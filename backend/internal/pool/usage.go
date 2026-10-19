package pool

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
)

// hourly 每小时一次：轮询订阅流量头并记录来源快照，用于流量与提供方趋势图
func (s *Service) hourly(ctx context.Context) error {
	hour := utcNow().Truncate(time.Hour)
	key := hour.Format("0601021504")[:8] // YYMMDDHH，适配 10 位 day 列
	res, err := s.Store.DB.ExecContext(ctx, `INSERT IGNORE INTO np_daily_jobs(kind,day,created_at) VALUES('hourly',?,?)`, key, utcNow())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if settings, err := s.Store.Settings(ctx); err == nil && settings.Community.UsagePollEnabled {
		s.pollUsage(ctx)
	}
	if err = s.Store.SnapshotSources(ctx, hour); err != nil {
		return err
	}
	cutoff := hour.Add(-36 * 24 * time.Hour)
	_, _ = s.Store.DB.ExecContext(ctx, `DELETE FROM np_source_hourly WHERE hour<?`, cutoff)
	_, _ = s.Store.DB.ExecContext(ctx, `DELETE FROM np_daily_jobs WHERE kind='hourly' AND created_at<?`, cutoff)
	if _, err = s.Store.PruneOrphanNodes(ctx, OrphanNodeRetention); err != nil {
		return err
	}
	return s.Store.PruneSessions(ctx)
}

// OrphanNodeRetention 节点不再属于任何来源后保留的时长，期间上游恢复可沿用原节点ID
const OrphanNodeRetention = 7 * 24 * time.Hour

// PruneOrphanNodes 标记、恢复并清理不再属于任何来源的去重节点
func (s *Store) PruneOrphanNodes(ctx context.Context, keep time.Duration) (int64, error) {
	now := utcNow()
	linked := `EXISTS(SELECT 1 FROM np_source_nodes sn WHERE sn.node_id=np_nodes.id)`
	if _, err := s.DB.ExecContext(ctx, `UPDATE np_nodes SET orphaned_at=NULL WHERE orphaned_at IS NOT NULL AND `+linked); err != nil {
		return 0, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE np_nodes SET orphaned_at=? WHERE orphaned_at IS NULL AND NOT `+linked, now); err != nil {
		return 0, err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM np_nodes WHERE orphaned_at<? AND NOT `+linked, now.Add(-keep))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// pollUsage 只读取响应头，不下载订阅正文
func (s *Service) pollUsage(ctx context.Context) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id FROM np_sources WHERE kind='url' AND enabled=1 AND (last_refresh_at IS NULL OR last_refresh_at<?)`, utcNow().Add(-30*time.Minute))
	if err != nil {
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, id := range ids {
		src, err := s.Store.Source(ctx, id)
		if err != nil || src.URL == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(src Source) {
			defer func() { <-sem; wg.Done() }()
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			client := s.UsageClient
			if client == nil {
				client = s.Client
			}
			usage := fetchUsage(c, client, src.URL)
			if usage == nil {
				return
			}
			fetched := utcNow()
			usage.FetchedAt = &fetched
			raw, _ := json.Marshal(usage)
			_, _ = s.Store.DB.ExecContext(c, `UPDATE np_sources SET subscription_usage=? WHERE id=?`, raw, src.ID)
		}(src)
	}
	wg.Wait()
}

// SnapshotSources 记录整点快照。已用流量在 Go 里解析：套餐信息缺少上传或下载字段时
// JSON 里是 null，严格模式下 CAST 会报错并让整条 INSERT 失败，导致所有来源都没有快照
func (s *Store) SnapshotSources(ctx context.Context, hour time.Time) error {
	used, err := s.usedBytes(ctx)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO np_source_hourly(source_id,hour,users,nodes,healthy,used_bytes)
 SELECT s.id,?,
 (SELECT COUNT(*) FROM np_source_seats st JOIN np_users su ON su.id=st.user_id AND su.enabled=1 WHERE st.source_id=s.id AND st.user_id<>s.owner_id AND st.state='active' AND st.expires_at>UTC_TIMESTAMP(6)),
 (SELECT COUNT(*) FROM np_source_nodes sn WHERE sn.source_id=s.id AND sn.excluded=0),
 (SELECT COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=s.id AND sn.excluded=0 AND n.blocked=0 AND n.status='healthy'),
 NULL
 FROM np_sources s
 ON DUPLICATE KEY UPDATE users=VALUES(users),nodes=VALUES(nodes),healthy=VALUES(healthy),used_bytes=NULL`, hour); err != nil {
		return err
	}
	ids := make([]int64, 0, len(used))
	for id := range used {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		cases, args := strings.Builder{}, make([]any, 0, len(chunk)*3+1)
		for _, id := range chunk {
			cases.WriteString(" WHEN ? THEN ?")
			args = append(args, id, used[id])
		}
		args = append(args, hour)
		for _, id := range chunk {
			args = append(args, id)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE np_source_hourly SET used_bytes=CASE source_id`+cases.String()+` END WHERE hour=? AND source_id IN (?`+strings.Repeat(",?", len(chunk)-1)+`)`, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// usedBytes 返回上传与下载都已知的来源的已用流量
func (s *Store) usedBytes(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,subscription_usage FROM np_sources WHERE subscription_usage IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id int64
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var u SubscriptionUsage
		if json.Unmarshal(raw, &u) != nil || u.UploadBytes == nil || u.DownloadBytes == nil || *u.UploadBytes > math.MaxInt64-*u.DownloadBytes {
			continue
		}
		out[id] = *u.UploadBytes + *u.DownloadBytes
	}
	return out, rows.Err()
}

type TrafficPoint struct {
	At    time.Time `json:"at"`
	Bytes int64     `json:"bytes"`
}

// TrafficStatistics 由每小时快照的已用流量差值得出；流量重置时取当前值
type TrafficStatistics struct {
	Hourly []TrafficPoint `json:"hourly"`
	Daily  []TrafficPoint `json:"daily"`
}

type hourlyRow struct {
	source int64
	hour   time.Time
	users  int
	nodes  int
	health int
	used   *int64
}

func (s *Store) hourlyRows(ctx context.Context, from time.Time, owner int64) ([]hourlyRow, error) {
	q := `SELECT h.source_id,h.hour,h.users,h.nodes,h.healthy,h.used_bytes FROM np_source_hourly h`
	args := []any{from}
	if owner > 0 {
		q += ` JOIN np_sources s ON s.id=h.source_id AND s.owner_id=?`
		args = []any{owner, from}
	}
	rows, err := s.DB.QueryContext(ctx, q+` WHERE h.hour>=? ORDER BY h.source_id,h.hour`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hourlyRow{}
	for rows.Next() {
		var r hourlyRow
		if err := rows.Scan(&r.source, &r.hour, &r.users, &r.nodes, &r.health, &r.used); err != nil {
			return nil, err
		}
		r.hour = r.hour.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// trafficDeltas 返回每条快照相对上一小时的增量，键为快照时间
func trafficDeltas(rows []hourlyRow) map[int64]map[time.Time]int64 {
	out := map[int64]map[time.Time]int64{}
	var prev *hourlyRow
	for i := range rows {
		r := &rows[i]
		if prev != nil && prev.source == r.source && prev.used != nil && r.used != nil && r.hour.Sub(prev.hour) <= 3*time.Hour {
			d := *r.used - *prev.used
			if d < 0 {
				d = *r.used
			}
			if out[r.source] == nil {
				out[r.source] = map[time.Time]int64{}
			}
			out[r.source][r.hour] = d
		}
		prev = r
	}
	return out
}

func (s *Store) TrafficStatistics(ctx context.Context) (TrafficStatistics, error) {
	now := utcNow().Truncate(time.Hour)
	today := dayStart(now)
	from := today.AddDate(0, 0, -29).Add(-time.Hour)
	rows, err := s.hourlyRows(ctx, from.UTC(), 0)
	if err != nil {
		return TrafficStatistics{}, err
	}
	hourly, daily := map[time.Time]int64{}, map[time.Time]int64{}
	for _, m := range trafficDeltas(rows) {
		for at, d := range m {
			// 快照记录的是截至该整点的累计值，增量归属上一小时
			slot := at.Add(-time.Hour)
			hourly[slot] += d
			daily[dayStart(slot)] += d
		}
	}
	out := TrafficStatistics{Hourly: make([]TrafficPoint, 0, 24), Daily: make([]TrafficPoint, 0, 30)}
	for i := 24; i >= 1; i-- {
		at := now.Add(-time.Duration(i) * time.Hour)
		out.Hourly = append(out.Hourly, TrafficPoint{at, hourly[at]})
	}
	for i := 29; i >= 0; i-- {
		at := today.AddDate(0, 0, -i)
		out.Daily = append(out.Daily, TrafficPoint{at.UTC(), daily[at]})
	}
	return out, nil
}

type TrendPoint struct {
	Day     time.Time `json:"day"`
	Users   int       `json:"users"`
	Nodes   int       `json:"nodes"`
	Healthy int       `json:"healthy"`
	Bytes   int64     `json:"bytes"`
	// Sampled 当天有快照；来源上架前的日子为 false，前端平均值不计入
	Sampled bool `json:"sampled"`
}

// ProviderTrend 汇总某用户提供的全部来源：每日取最后一次快照的人数与节点，流量取当日增量
func (s *Store) ProviderTrend(ctx context.Context, owner int64, days int) ([]TrendPoint, error) {
	today := dayStart(utcNow())
	from := today.AddDate(0, 0, -(days - 1)).Add(-time.Hour)
	rows, err := s.hourlyRows(ctx, from.UTC(), owner)
	if err != nil {
		return nil, err
	}
	type key struct {
		source int64
		day    time.Time
	}
	last := map[key]hourlyRow{}
	for _, r := range rows {
		k := key{r.source, dayStart(r.hour)}
		if v, ok := last[k]; !ok || r.hour.After(v.hour) {
			last[k] = r
		}
	}
	byDay := map[time.Time]*TrendPoint{}
	point := func(day time.Time) *TrendPoint {
		if byDay[day] == nil {
			byDay[day] = &TrendPoint{Day: day.UTC()}
		}
		return byDay[day]
	}
	for k, r := range last {
		p := point(k.day)
		p.Sampled = true
		p.Users += r.users
		p.Nodes += r.nodes
		p.Healthy += r.health
	}
	for _, m := range trafficDeltas(rows) {
		for at, d := range m {
			point(dayStart(at.Add(-time.Hour))).Bytes += d
		}
	}
	out := make([]TrendPoint, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i)
		if p := byDay[day]; p != nil {
			out = append(out, *p)
		} else {
			out = append(out, TrendPoint{Day: day.UTC()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

// fetchUsage 读取订阅的 Subscription-Userinfo：先用 HEAD 只取响应头，
// 面板不支持时退回 GET 并立即断开
func fetchUsage(ctx context.Context, client *http.Client, url string) *SubscriptionUsage {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		detail := strings.ToLower(method)
		req, err := http.NewRequestWithContext(traffic.WithDetail(ctx, detail), method, url, nil)
		if err != nil {
			return nil
		}
		req.Header.Set("User-Agent", "clash.meta")
		counter := traffic.Default.Counter(traffic.KindUsage, detail)
		resp, err := client.Do(req)
		if err != nil {
			counter.Hit(true)
			continue
		}
		// GET 回退不读正文，断开前内核已收下的部分不在计数里
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			if usage := ParseSubscriptionUsage(resp.Header.Get("Subscription-Userinfo")); usage != nil {
				counter.Hit(false)
				return usage
			}
		}
		counter.Hit(true)
	}
	return nil
}
