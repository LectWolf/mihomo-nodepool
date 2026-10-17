package pool

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"
)

// Match the worker eligibility rules, including explicit retests of blocked nodes.
const sourceWork = `(pending=1 OR (kind='url' AND next_refresh_at<=UTC_TIMESTAMP(6)) OR status='processing')`
const nodeWork = `(n.pending=1 OR n.next_check_at<=UTC_TIMESTAMP(6) OR n.lease_until IS NOT NULL)`
const nodeWorkEligible = `(n.blocked=0 OR n.pending=1) AND EXISTS(SELECT 1 FROM np_source_nodes sn JOIN np_sources src ON src.id=sn.source_id WHERE sn.node_id=n.id AND sn.excluded=0 AND src.enabled=1)`

type Statistics struct {
	SampledAt time.Time          `json:"sampled_at"`
	Overview  StatisticsOverview `json:"overview"`
	Tasks     Tasks              `json:"tasks"`
	Sources   []TaskItem         `json:"source_tasks"`
	Nodes     []TaskItem         `json:"node_tasks"`
	Usage     UsageStatistics    `json:"usage"`
	Traffic   TrafficStatistics  `json:"traffic"`
}

type StatisticsOverview struct {
	SourcesTotal   int `json:"sources_total"`
	SourcesEnabled int `json:"sources_enabled"`
	SourcesReady   int `json:"sources_ready"`
	SourcesError   int `json:"sources_error"`
	NodesTotal     int `json:"nodes_total"`
	NodesVisible   int `json:"nodes_visible"`
	NodesHealthy   int `json:"nodes_healthy"`
	NodesUnhealthy int `json:"nodes_unhealthy"`
	NodesUnknown   int `json:"nodes_unknown"`
	NodesBlocked   int `json:"nodes_blocked"`
	UsersTotal     int `json:"users_total"`
	UsersEnabled   int `json:"users_enabled"`
	ProvidersTotal int `json:"providers_total"`
	Subscriptions  int `json:"subscriptions"`
	ActiveSeats    int `json:"active_seats"`
}

type TaskItem struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Status     string     `json:"status"`
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
	NextAt     *time.Time `json:"next_at,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

type UsageStatistics struct {
	SourcesKnown     int               `json:"sources_known"`
	SourcesTotal     int               `json:"sources_total"`
	UploadBytes      *int64            `json:"upload_bytes"`
	DownloadBytes    *int64            `json:"download_bytes"`
	TotalBytes       *int64            `json:"total_bytes"`
	RemainingBytes   *int64            `json:"remaining_bytes"`
	UploadSources    int               `json:"upload_sources"`
	DownloadSources  int               `json:"download_sources"`
	TotalSources     int               `json:"total_sources"`
	RemainingSources int               `json:"remaining_sources"`
	Overflow         bool              `json:"overflow"`
	Items            []SourceUsageStat `json:"items"`
}

type SourceUsageStat struct {
	ID      int64              `json:"id"`
	Name    string             `json:"name"`
	Enabled bool               `json:"enabled"`
	Usage   *SubscriptionUsage `json:"usage"`
}

func (s *Store) Statistics(ctx context.Context) (Statistics, error) {
	out := Statistics{SampledAt: utcNow()}
	o := &out.Overview
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(enabled=1),0), COALESCE(SUM(enabled=1 AND status='ready'),0), COALESCE(SUM(enabled=1 AND status='error'),0) FROM np_sources`).Scan(&o.SourcesTotal, &o.SourcesEnabled, &o.SourcesReady, &o.SourcesError); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(blocked=1),0) FROM np_nodes`).Scan(&o.NodesTotal, &o.NodesBlocked); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status='healthy'),0), COALESCE(SUM(status='unhealthy'),0), COALESCE(SUM(status='unknown'),0) FROM np_nodes n WHERE `+visible).Scan(&o.NodesVisible, &o.NodesHealthy, &o.NodesUnhealthy, &o.NodesUnknown); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(enabled=1),0) FROM np_users`).Scan(&o.UsersTotal, &o.UsersEnabled); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT u.id) FROM np_users u JOIN np_sources src ON src.owner_id=u.id WHERE u.enabled=1 AND src.enabled=1`).Scan(&o.ProvidersTotal); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_subscriptions`).Scan(&o.Subscriptions); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM np_source_seats st JOIN np_users u ON u.id=st.user_id WHERE st.state='active' AND u.enabled=1 AND st.expires_at>UTC_TIMESTAMP(6)`).Scan(&o.ActiveSeats); err != nil {
		return out, err
	}
	var err error
	if out.Tasks, err = s.TaskStatus(ctx); err != nil {
		return out, err
	}
	if out.Sources, err = s.statisticsTasks(ctx, false); err != nil {
		return out, err
	}
	if out.Nodes, err = s.statisticsTasks(ctx, true); err != nil {
		return out, err
	}
	if out.Usage, err = s.statisticsUsage(ctx); err != nil {
		return out, err
	}
	out.Traffic, err = s.cachedTraffic(ctx)
	return out, err
}

var trafficCache struct {
	sync.Mutex
	at    time.Time
	value TrafficStatistics
}

// 运行统计每几秒刷新一次，流量图按快照小时变化，缓存 5 分钟即可
func (s *Store) cachedTraffic(ctx context.Context) (TrafficStatistics, error) {
	trafficCache.Lock()
	defer trafficCache.Unlock()
	if time.Since(trafficCache.at) < 5*time.Minute && trafficCache.value.Hourly != nil {
		return trafficCache.value, nil
	}
	v, err := s.TrafficStatistics(ctx)
	if err == nil {
		trafficCache.at, trafficCache.value = time.Now(), v
	}
	return v, err
}

func (s *Store) statisticsTasks(ctx context.Context, nodes bool) ([]TaskItem, error) {
	query := `SELECT id,name,kind,IF(lease_until>=UTC_TIMESTAMP(6),'processing','queued'),lease_until,next_refresh_at,last_error FROM np_sources WHERE enabled=1 AND (` + sourceWork + ` OR lease_until>=UTC_TIMESTAMP(6)) ORDER BY (lease_until>=UTC_TIMESTAMP(6)) DESC,pending DESC,next_refresh_at,id LIMIT 100`
	if nodes {
		query = `SELECT n.id,n.name,n.type,IF(n.lease_until>=UTC_TIMESTAMP(6),'processing','queued'),n.lease_until,n.next_check_at,n.last_error FROM np_nodes n WHERE ` + nodeWorkEligible + ` AND (` + nodeWork + ` OR n.lease_until>=UTC_TIMESTAMP(6)) ORDER BY (n.lease_until>=UTC_TIMESTAMP(6)) DESC,n.pending DESC,n.next_check_at,n.id LIMIT 100`
	}
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskItem{}
	for rows.Next() {
		var item TaskItem
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.Status, &item.LeaseUntil, &item.NextAt, &item.LastError); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) statisticsUsage(ctx context.Context) (UsageStatistics, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,enabled,subscription_usage FROM np_sources ORDER BY id DESC`)
	if err != nil {
		return UsageStatistics{}, err
	}
	defer rows.Close()
	out := UsageStatistics{Items: []SourceUsageStat{}}
	var upload, download, total, remaining byteTotal
	for rows.Next() {
		var item SourceUsageStat
		var raw []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Enabled, &raw); err != nil {
			return out, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &item.Usage); err != nil {
				return out, err
			}
		}
		refreshUsageStatus(item.Usage)
		out.SourcesTotal++
		out.Items = append(out.Items, item)
		if item.Usage == nil {
			continue
		}
		out.SourcesKnown++
		// Invalid metadata remains visible per source, but does not pollute totals.
		if item.Usage.Status != "invalid" {
			upload.add(item.Usage.UploadBytes)
			download.add(item.Usage.DownloadBytes)
			total.add(item.Usage.TotalBytes)
			remaining.add(item.Usage.RemainingBytes)
		}
	}
	out.UploadBytes, out.DownloadBytes, out.TotalBytes, out.RemainingBytes = upload.result(), download.result(), total.result(), remaining.result()
	out.UploadSources, out.DownloadSources, out.TotalSources, out.RemainingSources = upload.count, download.count, total.count, remaining.count
	out.Overflow = upload.overflow || download.overflow || total.overflow || remaining.overflow
	return out, rows.Err()
}

type byteTotal struct {
	value    int64
	count    int
	overflow bool
}

func (b *byteTotal) add(value *int64) {
	if value == nil || *value < 0 {
		return
	}
	b.count++
	if b.overflow || b.value > math.MaxInt64-*value {
		b.overflow = true
		return
	}
	b.value += *value
}
func (b byteTotal) result() *int64 {
	if b.count == 0 || b.overflow {
		return nil
	}
	return &b.value
}

// Public 去掉错误详情和停用来源，供普通用户与访客查看
func (v Statistics) Public() Statistics {
	strip := func(items []TaskItem) []TaskItem {
		out := make([]TaskItem, len(items))
		for i, t := range items {
			t.LastError = ""
			out[i] = t
		}
		return out
	}
	v.Sources, v.Nodes = strip(v.Sources), strip(v.Nodes)
	items := []SourceUsageStat{}
	for _, it := range v.Usage.Items {
		if it.Enabled {
			items = append(items, it)
		}
	}
	v.Usage.Items = items
	return v
}
