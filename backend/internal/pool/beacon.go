package pool

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// 客户端实测回报：订阅里为部分节点各加一个隐藏的 url-test 分组，检测地址指向本站。
// 节点在用户网络中能连通时，请求经该节点到达本站并记录；同一批次其它节点已回报而它没有，视为该用户连不上。
const (
	BeaconInterval = time.Hour          // 客户端检测间隔
	BeaconRotate   = time.Hour          // 同一订阅在此时长内复用同一批节点，超过后换一批
	BeaconSettle   = 10 * time.Minute   // 批次首次回报后等待其它节点回报的时间
	BeaconKeep     = 8 * 24 * time.Hour // 比最长统计窗口（7 天）多一天
	BeaconMaxNodes = 15
)

// FieldTest 是统计窗口内国内用户客户端的实测结果（按用户去重）
type FieldTest struct {
	OK   int `json:"ok"`
	Fail int `json:"fail"`
	// Days 统计窗口天数；Paused 表示国内用户近期能连，本机暂停检测延迟
	Days   int  `json:"days,omitempty"`
	Paused bool `json:"paused,omitempty"`
}

func (s *Store) ensureBeacon(ctx context.Context) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_users' AND COLUMN_NAME='beacon_disabled'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := s.DB.ExecContext(ctx, `ALTER TABLE np_users ADD COLUMN beacon_disabled TINYINT(1) NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS np_beacon_batches (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT NOT NULL, subscription_id BIGINT NOT NULL,
 region CHAR(2) NOT NULL DEFAULT '', created_at DATETIME(6) NOT NULL, first_ok_at DATETIME(6) NULL,
 INDEX(subscription_id,created_at), INDEX(user_id,created_at), INDEX(created_at),
 FOREIGN KEY(user_id) REFERENCES np_users(id) ON DELETE CASCADE,
 FOREIGN KEY(subscription_id) REFERENCES np_subscriptions(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS np_beacon_checks (
 batch_id BIGINT NOT NULL, node_id BIGINT NOT NULL, token CHAR(32) NOT NULL, ok_at DATETIME(6) NULL,
 PRIMARY KEY(batch_id,node_id), UNIQUE KEY(token), INDEX(node_id,batch_id),
 FOREIGN KEY(batch_id) REFERENCES np_beacon_batches(id) ON DELETE CASCADE,
 FOREIGN KEY(node_id) REFERENCES np_nodes(id) ON DELETE CASCADE
 ) ENGINE=InnoDB`,
	} {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// SetBeacon 开关当前用户的实测回报，客户端下次更新订阅时生效
func (s *Store) SetBeacon(ctx context.Context, userID int64, enabled bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE np_users SET beacon_disabled=? WHERE id=?`, !enabled, userID)
	return err
}

// BeaconTokens 为本次订阅选出要回报的节点及各自的令牌，cn 表示拉取订阅的客户端位于中国大陆
func (s *Store) BeaconTokens(ctx context.Context, token string, nodes []Node, cn bool) (map[int64]string, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	var subID, userID int64
	var disabled bool
	err := s.DB.QueryRowContext(ctx, `SELECT s.id,s.user_id,u.beacon_disabled FROM np_subscriptions s JOIN np_users u ON u.id=s.user_id WHERE s.token=?`, token).Scan(&subID, &userID, &disabled)
	if err != nil || disabled {
		return nil, err
	}
	present := map[int64]bool{}
	ids := []int64{}
	for _, n := range nodes {
		if !present[n.ID] {
			present[n.ID] = true
			ids = append(ids, n.ID)
		}
	}
	now := utcNow()
	var batch int64
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM np_beacon_batches WHERE subscription_id=? AND created_at>=? ORDER BY id DESC LIMIT 1`, subID, now.Add(-BeaconRotate)).Scan(&batch)
	if err == nil {
		out, err := s.batchTokens(ctx, batch, present)
		if err != nil || len(out) > 0 {
			return out, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	picked, err := s.pickBeaconNodes(ctx, userID, ids, now)
	if err != nil || len(picked) == 0 {
		return nil, err
	}
	region := ""
	if cn {
		region = "CN"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO np_beacon_batches(user_id,subscription_id,region,created_at) VALUES(?,?,?,?)`, userID, subID, region, now)
	if err != nil {
		return nil, err
	}
	if batch, err = res.LastInsertId(); err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, id := range picked {
		b := make([]byte, 16)
		if _, err = rand.Read(b); err != nil {
			return nil, err
		}
		t := hex.EncodeToString(b)
		if _, err = tx.ExecContext(ctx, `INSERT INTO np_beacon_checks(batch_id,node_id,token) VALUES(?,?,?)`, batch, id, t); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, tx.Commit()
}

func (s *Store) batchTokens(ctx context.Context, batch int64, present map[int64]bool) (map[int64]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT node_id,token FROM np_beacon_checks WHERE batch_id=?`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var t string
		if err = rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		if present[id] {
			out[id] = t
		}
	}
	return out, rows.Err()
}

// pickBeaconNodes 优先选该用户近期测得少的节点（没测过的最先），其次全站实测人数少的，同等条件随机
func (s *Store) pickBeaconNodes(ctx context.Context, userID int64, ids []int64, now time.Time) ([]int64, error) {
	type stat struct{ mine, users int }
	stats := map[int64]stat{}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := []any{userID, now.Add(-s.fieldWindow(ctx))}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT c.node_id,SUM(b.user_id=?),COUNT(DISTINCT b.user_id) FROM np_beacon_checks c JOIN np_beacon_batches b ON b.id=c.batch_id WHERE b.created_at>=? AND c.node_id IN (`+placeholders(len(chunk))+`) GROUP BY c.node_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var v stat
			if err = rows.Scan(&id, &v.mine, &v.users); err != nil {
				rows.Close()
				return nil, err
			}
			stats[id] = v
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	order := append([]int64{}, ids...)
	mrand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	sort.SliceStable(order, func(i, j int) bool {
		a, b := stats[order[i]], stats[order[j]]
		if a.mine != b.mine {
			return a.mine < b.mine
		}
		return a.users < b.users
	})
	if len(order) > BeaconMaxNodes {
		order = order[:BeaconMaxNodes]
	}
	return order, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// beaconSeen 记住已回报的令牌，客户端重复检测时不再访问数据库
var beaconSeen = struct {
	sync.Mutex
	m map[string]struct{}
}{m: map[string]struct{}{}}

// RecordBeacon 记录一次回报；未知令牌直接忽略
func (s *Store) RecordBeacon(ctx context.Context, token string) error {
	if len(token) != 32 {
		return nil
	}
	if _, err := hex.DecodeString(token); err != nil {
		return nil
	}
	beaconSeen.Lock()
	_, seen := beaconSeen.m[token]
	beaconSeen.Unlock()
	if seen {
		return nil
	}
	var batch int64
	var ok sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT batch_id,ok_at FROM np_beacon_checks WHERE token=?`, token).Scan(&batch, &ok)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ok.Valid {
		now := utcNow()
		if _, err = s.DB.ExecContext(ctx, `UPDATE np_beacon_checks SET ok_at=? WHERE token=? AND ok_at IS NULL`, now, token); err != nil {
			return err
		}
		if _, err = s.DB.ExecContext(ctx, `UPDATE np_beacon_batches SET first_ok_at=? WHERE id=? AND first_ok_at IS NULL`, now, batch); err != nil {
			return err
		}
	}
	beaconSeen.Lock()
	if len(beaconSeen.m) >= 200000 {
		beaconSeen.m = map[string]struct{}{}
	}
	beaconSeen.m[token] = struct{}{}
	beaconSeen.Unlock()
	return nil
}

// beaconResults 按 节点→用户 汇总近 72 小时的结果：true 为连通过，false 为同批次其它节点已回报而它没有
func (s *Store) beaconResults(ctx context.Context, where string, args []any, ids []int64) (map[int64]map[int64]bool, error) {
	out := map[int64]map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	now := utcNow()
	q := append(append([]any{}, args...), now.Add(-s.fieldWindow(ctx)), now.Add(-BeaconSettle))
	for _, id := range ids {
		q = append(q, id)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.node_id,b.user_id,MAX(c.ok_at IS NOT NULL) FROM np_beacon_checks c JOIN np_beacon_batches b ON b.id=c.batch_id WHERE `+where+` AND b.created_at>=? AND (c.ok_at IS NOT NULL OR b.first_ok_at<=?) AND c.node_id IN (`+placeholders(len(ids))+`) GROUP BY c.node_id,b.user_id`, q...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var node, user int64
		var ok bool
		if err = rows.Scan(&node, &user, &ok); err != nil {
			return out, err
		}
		if out[node] == nil {
			out[node] = map[int64]bool{}
		}
		out[node][user] = ok
	}
	return out, rows.Err()
}

// attachFieldTests 为节点列表补充国内用户实测结果
func (s *Store) attachFieldTests(ctx context.Context, items []Node) error {
	ids := make([]int64, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	res, err := s.beaconResults(ctx, `b.region='CN'`, nil, ids)
	if err != nil {
		return err
	}
	days := int(s.fieldWindow(ctx) / (24 * time.Hour))
	for i := range items {
		users := res[items[i].ID]
		if len(users) == 0 {
			continue
		}
		v := &FieldTest{Days: days}
		for _, ok := range users {
			if ok {
				v.OK++
			} else {
				v.Fail++
			}
		}
		v.Paused = items[i].Status == "healthy" && s.FieldTested(ctx, items[i].ID)
		items[i].FieldTest = v
	}
	return nil
}

// failedForUser 返回该用户客户端实测连不上的节点
func (s *Store) failedForUser(ctx context.Context, userID int64, ids []int64) (map[int64]bool, error) {
	res, err := s.beaconResults(ctx, `b.user_id=?`, []any{userID}, ids)
	out := map[int64]bool{}
	for id, users := range res {
		if ok, found := users[userID]; found && !ok {
			out[id] = true
		}
	}
	return out, err
}

// PruneBeacons 删除过期的回报批次
func (s *Store) PruneBeacons(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM np_beacon_batches WHERE created_at<?`, utcNow().Add(-BeaconKeep))
	return err
}

// AttachBeacons 为已导出的 Clash 配置追加隐藏的回报分组；base 形如 http://example.com
func AttachBeacons(raw []byte, nodes []Node, tokens map[int64]string, base string) ([]byte, error) {
	if len(tokens) == 0 {
		return raw, nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	proxies, _ := doc["proxies"].([]any)
	groups, _ := doc["proxy-groups"].([]any)
	byKey, reserved := map[string]string{}, map[string]bool{}
	for _, list := range [][]any{proxies, groups} {
		for _, v := range list {
			if m, ok := v.(map[string]any); ok {
				name := text(m["name"])
				reserved[name] = true
				if k := proxyKey(m); k != "" && byKey[k] == "" {
					byKey[k] = name
				}
			}
		}
	}
	added, done := 0, map[int64]bool{}
	for _, n := range nodes {
		t := tokens[n.ID]
		if t == "" || done[n.ID] {
			continue
		}
		done[n.ID] = true
		var m map[string]any
		if json.Unmarshal(n.Config, &m) != nil {
			continue
		}
		target := byKey[proxyKey(m)]
		if target == "" {
			continue
		}
		added++
		name := fmt.Sprintf("实测回报 %d", added)
		for reserved[name] {
			name += " ·"
		}
		reserved[name] = true
		groups = append(groups, map[string]any{"name": name, "type": "url-test", "proxies": []string{target}, "url": base + "/b/" + t, "interval": int(BeaconInterval / time.Second), "lazy": false, "hidden": true})
	}
	if added == 0 {
		return raw, nil
	}
	doc["proxy-groups"] = groups
	return yaml.Marshal(doc)
}

// proxyKey 用协议、地址、端口与凭据识别导出后改过名的节点
func proxyKey(m map[string]any) string {
	if text(m["server"]) == "" {
		return ""
	}
	return strings.Join([]string{text(m["type"]), text(m["server"]), text(m["port"]), text(m["uuid"]), text(m["password"]), text(m["username"])}, "|")
}
