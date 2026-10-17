package pool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var FeedbackKinds = map[string]map[string]string{
	"node":   {"unusable": "无法使用", "slow": "速度慢", "unstable": "不稳定", "ok": "可正常使用"},
	"source": {"expired": "订阅已过期", "many_down": "大量节点失效", "slow": "整体较慢", "ok": "整体可用"},
}

type FeedbackSummary struct {
	Total    int            `json:"total"`
	Bad      int            `json:"bad"`
	OK       int            `json:"ok"`
	Kinds    map[string]int `json:"kinds"`
	LatestAt *time.Time     `json:"latest_at,omitempty"`
}

type FeedbackInput struct {
	Target   string `json:"target"`
	NodeID   int64  `json:"node_id"`
	SourceID int64  `json:"source_id"`
	Kind     string `json:"kind"`
	Comment  string `json:"comment"`
}

type Feedback struct {
	ID             int64      `json:"id"`
	Target         string     `json:"target"`
	NodeID         int64      `json:"node_id"`
	SourceID       int64      `json:"source_id"`
	NodeName       string     `json:"node_name"`
	SourceName     string     `json:"source_name"`
	Kind           string     `json:"kind"`
	Comment        string     `json:"comment"`
	ProbeStatus    string     `json:"probe_status"`
	ProbeLatency   *int       `json:"probe_latency"`
	UsageDays      int        `json:"usage_days"`
	UserLevel      int        `json:"user_level"`
	Reporter       string     `json:"reporter"`
	ReporterAvatar int        `json:"reporter_avatar"`
	ReporterID     int64      `json:"reporter_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	CheckedOKAfter bool       `json:"checked_ok_after"`
	Mine           bool       `json:"mine"`
	CanResolve     bool       `json:"can_resolve"`
	CanDelete      bool       `json:"can_delete"`
}

type FeedbackFilter struct {
	Target         string
	NodeID         int64
	SourceID       int64
	Page, PageSize int
}

func cleanComment(s string) (string, error) {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if utf8.RuneCountInString(s) > 200 {
		return "", errors.New("补充说明最多 200 字")
	}
	return s, nil
}

func (s *Store) SubmitFeedback(ctx context.Context, in FeedbackInput) (Feedback, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Feedback{}, err
	}
	c := settings.Community
	if !c.FeedbackEnabled {
		return Feedback{}, errors.New("反馈功能已关闭")
	}
	kinds, ok := FeedbackKinds[in.Target]
	if !ok || kinds[in.Kind] == "" || in.SourceID <= 0 || (in.Target == "node") != (in.NodeID > 0) {
		return Feedback{}, errors.New("反馈类型无效")
	}
	if in.Comment, err = cleanComment(in.Comment); err != nil {
		return Feedback{}, err
	}
	u, err := s.User(ctx, CurrentUser(ctx).ID)
	if err != nil {
		return Feedback{}, err
	}
	now := utcNow()
	var owner int64
	var enabled bool
	if err = s.DB.QueryRowContext(ctx, `SELECT owner_id,enabled FROM np_sources WHERE id=?`, in.SourceID).Scan(&owner, &enabled); errors.Is(err, sql.ErrNoRows) {
		return Feedback{}, ErrNotFound
	} else if err != nil {
		return Feedback{}, err
	}
	if owner == u.ID {
		return Feedback{}, errors.New("不能反馈自己提供的来源")
	}
	usageDays := 0
	if u.Role != "admin" {
		var claimed time.Time
		err = s.DB.QueryRowContext(ctx, `SELECT claimed_at FROM np_source_seats WHERE source_id=? AND user_id=? AND state='active' AND expires_at>?`, in.SourceID, u.ID, now).Scan(&claimed)
		if errors.Is(err, sql.ErrNoRows) {
			return Feedback{}, fmt.Errorf("%w：只有正在使用该来源的用户可以反馈", ErrForbidden)
		}
		if err != nil {
			return Feedback{}, err
		}
		usageDays = int(now.Sub(claimed).Hours() / 24)
		if in.Target == "node" {
			subs, err := listSubscriptions(ctx, s.DB, u.ID)
			if err != nil {
				return Feedback{}, err
			}
			found := false
			for _, sub := range subs {
				for _, n := range sub.Nodes {
					if n.NodeID == in.NodeID && n.SourceID == in.SourceID {
						found = true
					}
				}
			}
			if !found {
				return Feedback{}, fmt.Errorf("%w：只有订阅中包含该节点的用户可以反馈", ErrForbidden)
			}
		}
	}
	probeStatus, latency := "", (*int)(nil)
	if in.Target == "node" {
		if err = s.DB.QueryRowContext(ctx, `SELECT n.status,n.latency_ms FROM np_nodes n JOIN np_source_nodes sn ON sn.node_id=n.id WHERE n.id=? AND sn.source_id=?`, in.NodeID, in.SourceID).Scan(&probeStatus, &latency); errors.Is(err, sql.ErrNoRows) {
			return Feedback{}, ErrNotFound
		} else if err != nil {
			return Feedback{}, err
		}
	} else {
		var healthy, total int
		if err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(n.status='healthy'),0),COUNT(*) FROM np_source_nodes sn JOIN np_nodes n ON n.id=sn.node_id WHERE sn.source_id=? AND sn.excluded=0 AND n.blocked=0`, in.SourceID).Scan(&healthy, &total); err != nil {
			return Feedback{}, err
		}
		probeStatus = fmt.Sprintf("%d/%d", healthy, total)
	}
	var today int
	if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_feedback WHERE user_id=? AND created_at>=?`, u.ID, dayStart(now)).Scan(&today); err != nil {
		return Feedback{}, err
	}
	var existing int64
	err = s.DB.QueryRowContext(ctx, `SELECT id FROM np_feedback WHERE user_id=? AND target=? AND node_id=? AND source_id=? AND created_at>=? ORDER BY id DESC LIMIT 1`, u.ID, in.Target, in.NodeID, in.SourceID, now.Add(-24*time.Hour)).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Feedback{}, err
	}
	if existing > 0 {
		// 24 小时内同一目标只保留一条，重复提交视为修改
		_, err = s.DB.ExecContext(ctx, `UPDATE np_feedback SET kind=?,comment=?,probe_status=?,probe_latency=?,usage_days=?,user_level=?,created_at=?,resolved_at=NULL,resolved_by=NULL WHERE id=?`, in.Kind, in.Comment, probeStatus, latency, usageDays, u.Level, now, existing)
	} else {
		if today >= c.FeedbackDailyLimit {
			return Feedback{}, fmt.Errorf("今日反馈次数已达上限（%d 次）", c.FeedbackDailyLimit)
		}
		var res sql.Result
		res, err = s.DB.ExecContext(ctx, `INSERT INTO np_feedback(target,node_id,source_id,user_id,kind,comment,probe_status,probe_latency,usage_days,user_level,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, in.Target, in.NodeID, in.SourceID, u.ID, in.Kind, in.Comment, probeStatus, latency, usageDays, u.Level, now)
		if err == nil {
			existing, err = res.LastInsertId()
		}
	}
	if err != nil {
		return Feedback{}, err
	}
	page, err := s.listFeedback(ctx, `f.id=?`, []any{existing}, 1, 1, 0)
	if err != nil || len(page.Items) == 0 {
		return Feedback{}, err
	}
	return page.Items[0], nil
}

type FeedbackPage struct {
	Items    []Feedback       `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Summary  *FeedbackSummary `json:"summary,omitempty"`
	Days     int              `json:"visible_days"`
}

func (s *Store) Feedback(ctx context.Context, f FeedbackFilter) (FeedbackPage, error) {
	c := currentCommunity()
	where, args := "", []any{}
	switch {
	case f.Target == "node" && f.NodeID > 0:
		where, args = `f.target='node' AND f.node_id=?`, []any{f.NodeID}
		if f.SourceID > 0 {
			where += ` AND f.source_id=?`
			args = append(args, f.SourceID)
		}
	case f.Target == "source" && f.SourceID > 0:
		where, args = `f.target='source' AND f.source_id=?`, []any{f.SourceID}
	case f.Target == "all" && f.SourceID > 0:
		where, args = `f.source_id=?`, []any{f.SourceID}
	default:
		return FeedbackPage{}, errors.New("请指定反馈对象")
	}
	page, size := pagination(f.Page, f.PageSize)
	return s.listFeedback(ctx, where, args, page, size, c.FeedbackVisibleDays)
}

func (s *Store) listFeedback(ctx context.Context, where string, args []any, page, size, days int) (FeedbackPage, error) {
	viewer := CurrentUser(ctx)
	out := FeedbackPage{Items: []Feedback{}, Page: page, PageSize: size, Days: days}
	if days > 0 {
		where += ` AND f.created_at>=?`
		args = append(args, utcNow().Add(-time.Duration(days)*24*time.Hour))
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_feedback f WHERE `+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT f.id,f.target,f.node_id,f.source_id,COALESCE((SELECT sn.node_name FROM np_source_nodes sn WHERE sn.node_id=f.node_id AND sn.source_id=f.source_id),''),s.name,s.owner_id,f.kind,f.comment,f.probe_status,f.probe_latency,f.usage_days,f.user_level,u.id,u.username,u.display_name,u.avatar_version,f.created_at,f.resolved_at,COALESCE((SELECT n.last_success_at>f.created_at FROM np_nodes n WHERE n.id=f.node_id),0)
 FROM np_feedback f JOIN np_sources s ON s.id=f.source_id JOIN np_users u ON u.id=f.user_id WHERE `+where+` ORDER BY f.created_at DESC,f.id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Feedback
		var owner int64
		var username, display string
		if err = rows.Scan(&v.ID, &v.Target, &v.NodeID, &v.SourceID, &v.NodeName, &v.SourceName, &owner, &v.Kind, &v.Comment, &v.ProbeStatus, &v.ProbeLatency, &v.UsageDays, &v.UserLevel, &v.ReporterID, &username, &display, &v.ReporterAvatar, &v.CreatedAt, &v.ResolvedAt, &v.CheckedOKAfter); err != nil {
			return out, err
		}
		privileged := viewer.Role == "admin" || viewer.ID == owner
		v.Mine = viewer.ID == v.ReporterID
		v.CanResolve = privileged
		v.CanDelete = viewer.Role == "admin" || v.Mine
		// 对其他用户匿名，只展示等级
		if privileged || v.Mine {
			v.Reporter = display
			if v.Reporter == "" {
				v.Reporter = username
			}
		} else {
			v.Reporter = fmt.Sprintf("Lv%d 用户", v.UserLevel)
			v.ReporterID, v.ReporterAvatar = 0, 0
		}
		if v.Target != "node" {
			v.CheckedOKAfter = false
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}

func (s *Store) feedbackOwner(ctx context.Context, id int64) (owner, author int64, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT s.owner_id,f.user_id FROM np_feedback f JOIN np_sources s ON s.id=f.source_id WHERE f.id=?`, id).Scan(&owner, &author)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) ResolveFeedback(ctx context.Context, id int64, resolved bool) error {
	owner, _, err := s.feedbackOwner(ctx, id)
	if err != nil {
		return err
	}
	viewer := CurrentUser(ctx)
	if viewer.Role != "admin" && viewer.ID != owner {
		return ErrForbidden
	}
	if resolved {
		if _, err = s.DB.ExecContext(ctx, `UPDATE np_feedback SET resolved_at=?,resolved_by=? WHERE id=?`, utcNow(), viewer.ID, id); err != nil {
			return err
		}
		return s.rewardFeedback(ctx, id, owner)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE np_feedback SET resolved_at=NULL,resolved_by=NULL WHERE id=?`, id)
	}
	return err
}

func (s *Store) DeleteFeedback(ctx context.Context, id int64) error {
	_, author, err := s.feedbackOwner(ctx, id)
	if err != nil {
		return err
	}
	viewer := CurrentUser(ctx)
	if viewer.Role != "admin" && viewer.ID != author {
		return ErrForbidden
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM np_feedback WHERE id=?`, id)
	return err
}

// feedbackSummaries counts unresolved feedback within the visible window.
func (s *Store) feedbackSummaries(ctx context.Context, target string, ids []int64) (map[int64]*FeedbackSummary, error) {
	out := map[int64]*FeedbackSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	c := currentCommunity()
	column := "source_id"
	if target == "node" {
		column = "node_id"
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{target, utcNow().Add(-time.Duration(c.FeedbackVisibleDays) * 24 * time.Hour)}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+column+`,kind,COUNT(*),MAX(created_at) FROM np_feedback WHERE target=? AND resolved_at IS NULL AND created_at>=? AND `+column+` IN (`+marks+`) GROUP BY `+column+`,kind`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var kind string
		var count int
		var latest time.Time
		if err = rows.Scan(&id, &kind, &count, &latest); err != nil {
			return out, err
		}
		v := out[id]
		if v == nil {
			v = &FeedbackSummary{Kinds: map[string]int{}}
			out[id] = v
		}
		v.Kinds[kind] = count
		v.Total += count
		if kind == "ok" {
			v.OK += count
		} else {
			v.Bad += count
		}
		if v.LatestAt == nil || latest.After(*v.LatestAt) {
			l := latest
			v.LatestAt = &l
		}
	}
	return out, rows.Err()
}

func (s *Store) attachNodeFeedback(ctx context.Context, items []Node) error {
	ids := make([]int64, 0, len(items))
	for _, n := range items {
		ids = append(ids, n.ID)
	}
	sums, err := s.feedbackSummaries(ctx, "node", ids)
	if err != nil {
		return err
	}
	srcIDs := []int64{}
	seen := map[int64]bool{}
	for i := range items {
		items[i].Feedback = sums[items[i].ID]
		for _, o := range items[i].Sources {
			if !seen[o.ID] {
				seen[o.ID] = true
				srcIDs = append(srcIDs, o.ID)
			}
		}
	}
	srcSums, err := s.feedbackSummaries(ctx, "source", srcIDs)
	if err != nil {
		return err
	}
	for i := range items {
		for j := range items[i].Sources {
			items[i].Sources[j].Feedback = srcSums[items[i].Sources[j].ID]
		}
	}
	return nil
}

func (s *Store) attachSourceFeedback(ctx context.Context, items []Source) error {
	ids := make([]int64, 0, len(items))
	for _, v := range items {
		ids = append(ids, v.ID)
	}
	sums, err := s.feedbackSummaries(ctx, "source", ids)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Feedback = sums[items[i].ID]
	}
	return nil
}

// rewardFeedback 提供方确认问题反馈后奖励反馈人；每条只奖励一次，并受每日次数限制
func (s *Store) rewardFeedback(ctx context.Context, id, owner int64) error {
	c := currentCommunity()
	if c.FeedbackRewardDaily <= 0 || (c.FeedbackRewardXP <= 0 && c.FeedbackRewardPoints <= 0) {
		return nil
	}
	var author int64
	var kind string
	if err := s.DB.QueryRowContext(ctx, `SELECT user_id,kind FROM np_feedback WHERE id=?`, id).Scan(&author, &kind); err != nil {
		return err
	}
	if kind == "ok" || author == owner {
		return nil
	}
	var today int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_ledger WHERE user_id=? AND kind='feedback' AND created_at>=?`, author, dayStart(utcNow())).Scan(&today); err != nil {
		return err
	}
	if today >= c.FeedbackRewardDaily {
		return nil
	}
	_, err := award(ctx, s.DB, author, "feedback", c.FeedbackRewardXP, c.FeedbackRewardPoints, id, "反馈被提供方确认", fmt.Sprintf("fb:%d", id))
	return err
}

// demotedNodes 返回 24 小时内被多名用户反馈异常且未处理的节点，订阅导出时排到最后
func (s *Store) demotedNodes(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	threshold := currentCommunity().FeedbackDemoteUsers
	if threshold <= 0 || len(ids) == 0 {
		return out, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{utcNow().Add(-24 * time.Hour)}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, threshold)
	rows, err := s.DB.QueryContext(ctx, `SELECT node_id FROM np_feedback WHERE target='node' AND kind IN ('unusable','unstable','slow') AND resolved_at IS NULL AND created_at>=? AND node_id IN (`+marks+`) GROUP BY node_id HAVING COUNT(DISTINCT user_id)>=?`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return out, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
