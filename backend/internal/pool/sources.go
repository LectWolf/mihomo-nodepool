package pool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func urlHash(raw string) any {
	if raw == "" {
		return nil
	}
	h := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(h[:])
}
func (s *Store) AddSource(ctx context.Context, in SourceInput) (Source, error) {
	defer s.InvalidateIndex()
	if e := checkSource(in); e != nil {
		return Source{}, e
	}
	now := utcNow()
	token, e := newToken()
	if e != nil {
		return Source{}, e
	}
	sealedURL, e := seal(purposeSourceURL, strings.TrimSpace(in.URL))
	if e != nil {
		return Source{}, e
	}
	sealedContent, e := seal(purposeSourceContent, in.Content)
	if e != nil {
		return Source{}, e
	}
	u := CurrentUser(ctx)
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Source{}, e
	}
	defer tx.Rollback()
	if u.Role != "admin" {
		var owned int
		if e = tx.QueryRowContext(ctx, `SELECT id FROM np_users WHERE id=? FOR UPDATE`, u.ID).Scan(new(int64)); e != nil {
			return Source{}, e
		}
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_sources WHERE owner_id=?`, u.ID).Scan(&owned); e != nil {
			return Source{}, e
		}
		if owned >= MaxOwnedSources {
			return Source{}, fmt.Errorf("每个账号最多提供 %d 个来源", MaxOwnedSources)
		}
	}
	result, e := tx.ExecContext(ctx, `INSERT INTO np_sources(owner_id,max_users,min_level,share_token,name,kind,url,url_hash,content,exclude_pattern,last_error,created_at,next_refresh_at) VALUES(?,?,?,?,?,?,?,?,?,?,'',?,?)`, u.ID, in.MaxUsers, in.MinLevel, token, cleanName(in.Name), in.Kind, sealedURL, urlHash(in.URL), sealedContent, in.ExcludePattern, now, now)
	if e != nil {
		var me *mysql.MySQLError
		if errors.As(e, &me) && me.Number == 1062 {
			return Source{}, errors.New("此订阅链接已存在")
		}
		return Source{}, e
	}
	id, e := result.LastInsertId()
	if e != nil {
		return Source{}, e
	}
	if e = tx.Commit(); e != nil {
		return Source{}, e
	}
	return s.Source(ctx, id)
}

// MaxOwnedSources 单个账号可提供的来源数上限（管理员不限）
const MaxOwnedSources = 100

func (s *Store) PatchSource(ctx context.Context, id int64, p SourcePatch) (Source, error) {
	defer s.InvalidateIndex()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Source{}, e
	}
	defer tx.Rollback()
	v, e := scanSource(tx.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM np_sources WHERE id=? FOR UPDATE`, id))
	if e == sql.ErrNoRows {
		return v, ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if u := CurrentUser(ctx); u.Role != "admin" && v.OwnerID != u.ID {
		return v, ErrForbidden
	}
	if p.MaxUsers != nil {
		if *p.MaxUsers < 0 {
			return v, errors.New("领取人数上限不能为负数")
		}
		v.MaxUsers = *p.MaxUsers
	}
	if p.MinLevel != nil {
		if *p.MinLevel < 0 || *p.MinLevel > 6 {
			return v, errors.New("最低等级须为Lv0–Lv6")
		}
		v.MinLevel = *p.MinLevel
	}
	if p.Name != nil {
		v.Name = cleanName(*p.Name)
	}
	if v.Name == "" || len(v.Name) > 200 {
		return v, errors.New("来源名称不能为空且最多 200 字节")
	}
	// 只有排除规则、内容、链接真正变化或重新启用时才重新解析，改名称和人数等不触发更新
	reparse := false
	if p.ExcludePattern != nil {
		if _, e := Pattern(*p.ExcludePattern); e != nil {
			return v, e
		}
		reparse = v.ExcludePattern != *p.ExcludePattern
		v.ExcludePattern = *p.ExcludePattern
	}
	fetch := false
	if p.URL != nil && strings.TrimSpace(*p.URL) != "" && strings.TrimSpace(*p.URL) != v.URL {
		if v.Kind != "url" {
			return v, errors.New("只有订阅来源可以修改 URL")
		}
		v.URL = strings.TrimSpace(*p.URL)
		if e := checkSource(SourceInput{Name: v.Name, Kind: v.Kind, URL: v.URL}); e != nil {
			return v, e
		}
		fetch = true
	}
	if p.Content != nil {
		if v.Kind == "url" {
			return v, errors.New("订阅来源请修改 URL，不能直接替换内容")
		}
		if e := checkSource(SourceInput{Name: v.Name, Kind: v.Kind, Content: *p.Content}); e != nil {
			return v, e
		}
		content, e := seal(purposeSourceContent, *p.Content)
		if e != nil {
			return v, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE np_sources SET content=? WHERE id=?`, content, id); e != nil {
			return v, e
		}
		reparse = true
	}
	if p.Enabled != nil {
		if *p.Enabled && v.Takedown != nil {
			return v, fmt.Errorf("%w（%s），请联系管理员恢复", ErrTakenDown, v.Takedown.ReasonLabel)
		}
		if *p.Enabled && !v.Enabled {
			reparse = true
		}
		v.Enabled = *p.Enabled
	}
	sealedURL, e := seal(purposeSourceURL, v.URL)
	if e != nil {
		return v, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE np_sources SET max_users=?,min_level=? WHERE id=?`, v.MaxUsers, v.MinLevel, id); e != nil {
		return v, e
	}
	// Evaluate cached before pending: queued/in-flight network work and due refreshes
	// take priority over reparsing old content. MySQL SET assignments run left to right.
	_, e = tx.ExecContext(ctx, `UPDATE np_sources SET name=?,enabled=?,exclude_pattern=?,url=?,url_hash=?,revision=revision+1,cached=IF(?,0,IF(?,IF(kind='url' AND ((cached=0 AND (pending=1 OR lease_until IS NOT NULL)) OR next_refresh_at<=UTC_TIMESTAMP(6)),0,1),cached)),pending=IF(? OR ?,1,pending),status=IF(? OR ?,'queued',status) WHERE id=?`, v.Name, v.Enabled, v.ExcludePattern, sealedURL, urlHash(v.URL), fetch, reparse, reparse, fetch, reparse, fetch, id)
	if e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	// 停用或调整等级要求后，受影响的订阅切换到其他来源
	if !v.Enabled || p.MinLevel != nil {
		s.rehomeSources(ctx, id)
	}
	if (p.Enabled != nil && *p.Enabled) || (p.MaxUsers != nil && *p.MaxUsers == 0) {
		resetRehome()
	}
	return s.Source(ctx, id)
}
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	defer s.InvalidateIndex()
	if e := s.SourcePermission(ctx, id); e != nil {
		return e
	}
	users, e := sourceUsers(ctx, s.DB, []int64{id})
	if e != nil {
		return e
	}
	r, e := s.DB.ExecContext(ctx, `DELETE FROM np_sources WHERE id=?`, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	s.rehomeUsers(ctx, users)
	return nil
}
func (s *Store) QueueSource(ctx context.Context, id int64) error {
	if e := s.SourcePermission(ctx, id); e != nil {
		return e
	}
	r, e := s.DB.ExecContext(ctx, `UPDATE np_sources SET pending=1,cached=0,status='queued' WHERE id=? AND enabled=1`, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return errors.New("来源不存在、已停用或已排队")
	}
	return nil
}

func (s *Store) ApplySource(ctx context.Context, src Source, body string, parsed ParseResult) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Same lock order as settings changes prevents stale rules and deadlocks.
	var settingsRaw []byte
	if e = tx.QueryRowContext(ctx, `SELECT body FROM np_settings WHERE id=1 FOR UPDATE`).Scan(&settingsRaw); e != nil {
		return e
	}
	settings := Defaults()
	if e = json.Unmarshal(settingsRaw, &settings); e != nil {
		return e
	}
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM np_sources WHERE id=? FOR UPDATE`, src.ID).Scan(&revision); e == sql.ErrNoRows {
		return nil
	} else if e != nil {
		return e
	}
	if revision != src.Revision {
		_, e = tx.ExecContext(ctx, `UPDATE np_sources SET lease_until=NULL WHERE id=?`, src.ID)
		if e != nil {
			return e
		}
		return tx.Commit()
	}
	global, e := Pattern(settings.ExcludePattern)
	if e != nil {
		return e
	}
	local, e := Pattern(src.ExcludePattern)
	if e != nil {
		return e
	}
	oldNames, e := sourceNodeNames(ctx, tx, src.ID)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM np_source_nodes WHERE source_id=?`, src.ID); e != nil {
		return e
	}
	insert, e := tx.PrepareContext(ctx, `INSERT INTO np_nodes(config_hash,name,type,server,port,config,last_error,created_at,next_check_at) VALUES(?,?,?,?,?,?,'',?,?) ON DUPLICATE KEY UPDATE id=LAST_INSERT_ID(id)`)
	if e != nil {
		return e
	}
	defer insert.Close()
	link, e := tx.PrepareContext(ctx, `INSERT INTO np_source_nodes(source_id,node_id,node_name,excluded) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE excluded=LEAST(excluded,VALUES(excluded)),node_name=IF(VALUES(excluded)=0,VALUES(node_name),node_name)`)
	if e != nil {
		return e
	}
	defer link.Close()
	now := utcNow()
	seen := map[string]bool{}
	for _, node := range parsed.Nodes {
		excluded := (global != nil && global.MatchString(node.Name)) || (local != nil && local.MatchString(node.Name))
		server, e := seal(purposeNodeServer, node.Server)
		if e != nil {
			return e
		}
		config, e := sealConfig([]byte(node.Config))
		if e != nil {
			return e
		}
		r, e := insert.ExecContext(ctx, node.Hash, node.Name, node.Type, server, node.Port, config, now, InitialRun(node.Hash, now))
		if e != nil {
			return e
		}
		nodeID, e := r.LastInsertId()
		if e != nil {
			return e
		}
		if _, e = link.ExecContext(ctx, src.ID, nodeID, node.Name, excluded); e != nil {
			return e
		}
		old, exists := seen[node.Hash]
		seen[node.Hash] = excluded && (!exists || old)
	}
	active, excluded := 0, 0
	for _, v := range seen {
		if v {
			excluded++
		} else {
			active++
		}
	}
	warning := ""
	if len(parsed.Warnings) > 0 {
		warning = fmt.Sprintf("已跳过 %d 条无法解析的记录", len(parsed.Warnings))
	}
	next := NextRun(src.CreatedAt, fmt.Sprintf("source:%d", src.ID), now)
	var nextAt any
	if src.Kind == "url" {
		nextAt = next
	}
	usage, e := json.Marshal(src.SubscriptionUsage)
	if e != nil {
		return e
	}
	sealedBody, e := seal(purposeSourceContent, body)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE np_sources SET content=?,subscription_usage=?,node_count=?,excluded_count=?,last_refresh_at=?,next_refresh_at=?,last_error=?,status=IF(pending=1,'queued','ready'),attempts=0,lease_until=NULL WHERE id=?`, sealedBody, usage, active, excluded, now, nextAt, warning, src.ID)
	if e != nil {
		return e
	}
	if active > 0 && src.OwnerID > 0 {
		if e = achievement(ctx, tx, src.OwnerID, "first_source", "成就：第一次成功共享来源"); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.InvalidateIndex()
	// 节点已保存，后续两步失败只影响排队和换绑，下次刷新会再做
	if e = s.RebindSelections(ctx, src.ID, oldNames); e != nil {
		log.Print("订阅节点同名换绑失败，下次刷新重试")
	}
	if e = s.QueueSourceNodes(ctx, src.ID, now); e != nil {
		log.Print("来源节点排队检测失败，节点按兜底周期检测")
	}
	return nil
}
func (s *Store) FailSource(ctx context.Context, src Source, message string) error {
	// Network diagnostics deliberately omit URLs and upstream bodies (subscription tokens).
	_, e := s.DB.ExecContext(ctx, `UPDATE np_sources SET status=IF(pending=1,'queued','error'),last_error=?,lease_until=NULL,attempts=attempts+1,next_refresh_at=IF(kind='url',?,NULL) WHERE id=? AND revision=?`, message, utcNow().Add(15*time.Minute), src.ID, src.Revision)
	return e
}
