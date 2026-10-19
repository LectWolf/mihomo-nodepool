package pool

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
)

// 本站流量：按小时累计服务自身的收发字节，保留 35 天
const siteTrafficKeepDays = 35

func (s *Store) ensureSiteTraffic(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS np_site_traffic (
 hour DATETIME NOT NULL, kind VARCHAR(16) NOT NULL, detail VARCHAR(32) NOT NULL DEFAULT '',
 requests BIGINT NOT NULL DEFAULT 0, failures BIGINT NOT NULL DEFAULT 0,
 bytes_in BIGINT NOT NULL DEFAULT 0, bytes_out BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(hour,kind,detail)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`)
	return err
}

func clipASCII(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// FlushSiteTraffic 把增量累加到所在小时
func (s *Store) FlushSiteTraffic(ctx context.Context, hour time.Time, rows []traffic.Row) error {
	if len(rows) == 0 {
		return nil
	}
	var b strings.Builder
	args := make([]any, 0, len(rows)*7)
	b.WriteString(`INSERT INTO np_site_traffic(hour,kind,detail,requests,failures,bytes_in,bytes_out) VALUES`)
	for i, r := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(?,?,?,?,?,?,?)")
		args = append(args, hour, clipASCII(r.Kind, 16), clipASCII(r.Detail, 32), r.Requests, r.Failures, r.In, r.Out)
	}
	b.WriteString(` ON DUPLICATE KEY UPDATE requests=requests+VALUES(requests),failures=failures+VALUES(failures),bytes_in=bytes_in+VALUES(bytes_in),bytes_out=bytes_out+VALUES(bytes_out)`)
	_, err := s.DB.ExecContext(ctx, b.String(), args...)
	return err
}

func (s *Store) pruneSiteTraffic(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM np_site_traffic WHERE hour<?`, utcNow().AddDate(0, 0, -siteTrafficKeepDays))
	return err
}

type SiteTrafficRow struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Detail   string    `json:"detail,omitempty"`
	Requests int64     `json:"requests"`
	Failures int64     `json:"failures"`
	In       int64     `json:"bytes_in"`
	Out      int64     `json:"bytes_out"`
}

type SiteTraffic struct {
	// Hourly 近 24 小时，按分类和明细；Daily 近 30 天，只按分类
	Hourly []SiteTrafficRow `json:"hourly"`
	Daily  []SiteTrafficRow `json:"daily"`
}

var siteTrafficCache struct {
	sync.Mutex
	at    time.Time
	value SiteTraffic
}

// SiteTraffic 读取数据库里的小时记录，并补上尚未写库的当前增量
func (s *Store) SiteTraffic(ctx context.Context) (SiteTraffic, error) {
	siteTrafficCache.Lock()
	defer siteTrafficCache.Unlock()
	if time.Since(siteTrafficCache.at) < 20*time.Second {
		return siteTrafficCache.value, nil
	}
	now := utcNow()
	hour := now.Truncate(time.Hour)
	out := SiteTraffic{Hourly: []SiteTrafficRow{}, Daily: []SiteTrafficRow{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT hour,kind,detail,requests,failures,bytes_in,bytes_out FROM np_site_traffic WHERE hour>=? ORDER BY hour,kind,detail`, hour.Add(-23*time.Hour))
	if err != nil {
		return out, err
	}
	idx := map[[3]string]int{}
	for rows.Next() {
		var r SiteTrafficRow
		if err := rows.Scan(&r.At, &r.Kind, &r.Detail, &r.Requests, &r.Failures, &r.In, &r.Out); err != nil {
			rows.Close()
			return out, err
		}
		r.At = r.At.UTC()
		idx[[3]string{r.At.Format(time.RFC3339), r.Kind, r.Detail}] = len(out.Hourly)
		out.Hourly = append(out.Hourly, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	pending := traffic.Default.Peek()
	for _, p := range pending {
		k := [3]string{hour.Format(time.RFC3339), p.Kind, p.Detail}
		if i, ok := idx[k]; ok {
			r := &out.Hourly[i]
			r.Requests += p.Requests
			r.Failures += p.Failures
			r.In += p.In
			r.Out += p.Out
			continue
		}
		out.Hourly = append(out.Hourly, SiteTrafficRow{At: hour, Kind: p.Kind, Detail: p.Detail, Requests: p.Requests, Failures: p.Failures, In: p.In, Out: p.Out})
	}
	// 按天汇总与套餐流量图使用同一个日界
	from := dayStart(hour).AddDate(0, 0, -29)
	drows, err := s.DB.QueryContext(ctx, `SELECT hour,kind,SUM(requests),SUM(failures),SUM(bytes_in),SUM(bytes_out) FROM np_site_traffic WHERE hour>=? GROUP BY hour,kind`, from.UTC())
	if err != nil {
		return out, err
	}
	defer drows.Close()
	type dayKey struct {
		at   int64
		kind string
	}
	daily := map[dayKey]*SiteTrafficRow{}
	add := func(at time.Time, kind string, v SiteTrafficRow) {
		k := dayKey{dayStart(at).Unix(), kind}
		r := daily[k]
		if r == nil {
			r = &SiteTrafficRow{At: dayStart(at).UTC(), Kind: kind}
			daily[k] = r
		}
		r.Requests += v.Requests
		r.Failures += v.Failures
		r.In += v.In
		r.Out += v.Out
	}
	for drows.Next() {
		var r SiteTrafficRow
		if err := drows.Scan(&r.At, &r.Kind, &r.Requests, &r.Failures, &r.In, &r.Out); err != nil {
			return out, err
		}
		add(r.At.UTC(), r.Kind, r)
	}
	if err := drows.Err(); err != nil {
		return out, err
	}
	for _, p := range pending {
		add(hour, p.Kind, SiteTrafficRow{Requests: p.Requests, Failures: p.Failures, In: p.In, Out: p.Out})
	}
	for _, r := range daily {
		out.Daily = append(out.Daily, *r)
	}
	sort.Slice(out.Daily, func(i, j int) bool {
		if !out.Daily[i].At.Equal(out.Daily[j].At) {
			return out.Daily[i].At.Before(out.Daily[j].At)
		}
		return out.Daily[i].Kind < out.Daily[j].Kind
	})
	siteTrafficCache.at, siteTrafficCache.value = time.Now(), out
	return out, nil
}

// siteTrafficLoop 每分钟写一次库；退出时尽量把最后的增量写进去
func (s *Service) siteTrafficLoop(ctx context.Context) {
	var nic traffic.NICSampler
	nic.Sample(traffic.Default)
	flush := func(c context.Context) {
		nic.Sample(traffic.Default)
		rows := traffic.Default.Drain()
		if len(rows) == 0 {
			return
		}
		if err := s.Store.FlushSiteTraffic(c, utcNow().Truncate(time.Hour), rows); err != nil {
			traffic.Default.Restore(rows)
			log.Print("本站流量写入失败，将在下次合并写入")
		}
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var pruned time.Time
	for {
		select {
		case <-ctx.Done():
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			flush(c)
			cancel()
			return
		case <-ticker.C:
			if !s.ready(ctx) {
				continue
			}
			flush(ctx)
			if time.Since(pruned) >= time.Hour {
				pruned = time.Now()
				_ = s.Store.pruneSiteTraffic(ctx)
			}
		}
	}
}
