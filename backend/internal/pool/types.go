package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const DefaultExclude = `(?i)(剩余|到期|过期|流量|官网|套餐|重置|expire|traffic|website|remaining)`
const Period = 6 * time.Hour

var ErrNotFound = errors.New("记录不存在")
var ErrUnavailable = errors.New("MySQL 尚未配置或暂不可用")

type Settings struct {
	ExcludePattern    string `json:"exclude_pattern"`
	TestURL           string `json:"test_url"`
	TimeoutMS         int    `json:"timeout_ms"`
	ProbeConcurrency  int    `json:"probe_concurrency"`
	SourceConcurrency int    `json:"source_concurrency"`
	RefreshHours      int    `json:"refresh_hours"`
	CheckHours        int    `json:"check_hours"`
}

func Defaults() Settings {
	return Settings{DefaultExclude, "https://www.gstatic.com/generate_204", 5000, 16, 5, 6, 6}
}
func (s Settings) Validate() error {
	if _, err := Pattern(s.ExcludePattern); err != nil {
		return err
	}
	u, err := url.Parse(s.TestURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return errors.New("测试地址必须是有效 HTTP(S) URL")
	}
	if s.TimeoutMS < 1000 || s.TimeoutMS > 30000 || s.ProbeConcurrency < 1 || s.ProbeConcurrency > 64 || s.SourceConcurrency < 1 || s.SourceConcurrency > 10 {
		return errors.New("超时须为 1000–30000 毫秒，检测并发 1–64，来源并发 1–10")
	}
	return nil
}
func Pattern(raw string) (*regexp.Regexp, error) {
	if len(raw) > 2000 {
		return nil, errors.New("排除正则最多 2000 字节")
	}
	if raw == "" {
		return nil, nil
	}
	r, e := regexp.Compile(raw)
	if e != nil {
		return nil, errors.New("排除正则无效")
	}
	return r, nil
}

type Source struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	Enabled        bool       `json:"enabled"`
	ExcludePattern string     `json:"exclude_pattern"`
	Status         string     `json:"status"`
	NodeCount      int        `json:"node_count"`
	ExcludedCount  int        `json:"excluded_count"`
	LastError      string     `json:"last_error"`
	LastRefreshAt  *time.Time `json:"last_refresh_at"`
	NextRefreshAt  *time.Time `json:"next_refresh_at"`
	CreatedAt      time.Time  `json:"created_at"`
	URL            string     `json:"-"`
	Content        string     `json:"-"`
	Revision       int64      `json:"-"`
	Cached         bool       `json:"-"`
}
type SourceInput struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	URL            string `json:"url"`
	Content        string `json:"content"`
	ExcludePattern string `json:"exclude_pattern"`
}
type SourcePatch struct {
	Name           *string `json:"name"`
	Enabled        *bool   `json:"enabled"`
	ExcludePattern *string `json:"exclude_pattern"`
	URL            *string `json:"url"`
}
type Origin struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	NodeName string `json:"node_name"`
}
type Node struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	Server        string          `json:"server"`
	Port          int             `json:"port"`
	LatencyMS     *int            `json:"latency_ms"`
	Status        string          `json:"status"`
	Country       string          `json:"country"`
	CountryCode   string          `json:"country_code"`
	Region        string          `json:"region"`
	City          string          `json:"city"`
	ExitIP        string          `json:"exit_ip"`
	LastCheckedAt *time.Time      `json:"last_checked_at"`
	NextCheckAt   time.Time       `json:"next_check_at"`
	LastError     string          `json:"last_error"`
	Sources       []Origin        `json:"sources"`
	Config        json.RawMessage `json:"-"`
	CreatedAt     time.Time       `json:"-"`
	Failures      int             `json:"-"`
	Successes     int             `json:"-"`
	ExitCheckedAt *time.Time      `json:"-"`
}
type ParsedNode struct {
	Name, Type, Server, Hash string
	Port                     int
	Config                   json.RawMessage
}
type ParseResult struct {
	Nodes    []ParsedNode
	Warnings []string
}
type Stats struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
	Pending   int `json:"pending"`
}
type NodePage struct {
	Items    []Node `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Stats    Stats  `json:"stats"`
}
type Filter struct {
	Page, PageSize                 int
	Q, Type, Country, Status, Sort string
	SourceID                       int64
	MaxLatency                     int
}
type Tasks struct {
	PendingSources int `json:"pending_sources"`
	PendingNodes   int `json:"pending_nodes"`
	ActiveSources  int `json:"active_sources"`
	ActiveNodes    int `json:"active_nodes"`
}
type ProbeResult struct {
	LatencyMS                    *int
	Error                        string
	ExitIP, Country, CountryCode string
	Region, City                 string
	ExitAttempted                bool
	ExitError                    string
}
type Prober interface {
	Probe(context.Context, Node, Settings, bool) ProbeResult
	Version() string
	Validate(map[string]any) error
}

// NextRun gives each item a stable phase relative to its own creation time.
// Missed cycles collapse into one future occurrence instead of catch-up jobs.
func NextRun(created time.Time, key string, now time.Time) time.Time {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	offset := time.Duration(h.Sum64() % uint64(Period))
	anchor := created.Add(offset)
	if anchor.After(now) {
		return anchor
	}
	return anchor.Add((now.Sub(anchor)/Period + 1) * Period)
}
func InitialRun(key string, now time.Time) time.Time {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return now.Add(time.Duration(h.Sum32()%30000) * time.Millisecond)
}
func cleanName(s string) string { return strings.TrimSpace(s) }
func checkSource(in SourceInput) error {
	if cleanName(in.Name) == "" || len(in.Name) > 200 {
		return errors.New("来源名称不能为空且最多 200 字节")
	}
	if _, err := Pattern(in.ExcludePattern); err != nil {
		return err
	}
	switch in.Kind {
	case "url":
		u, e := url.Parse(in.URL)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return errors.New("订阅地址必须是 HTTP(S) URL")
		}
	case "file", "links":
		if strings.TrimSpace(in.Content) == "" {
			return errors.New("导入内容不能为空")
		}
	default:
		return fmt.Errorf("不支持的来源类型")
	}
	if len(in.Content) > 16<<20 || len(in.URL) > 8000 {
		return errors.New("来源内容过大")
	}
	return nil
}
