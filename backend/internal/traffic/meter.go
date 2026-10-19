// Package traffic 统计本服务自身产生的网络流量，按功能分类累计，由 pool 定时写入数据库。
package traffic

import (
	"sort"
	"sync"
	"sync/atomic"
)

// 分类：对外服务的入站是请求、出站是响应；后台任务的入站是下载、出站是上传
const (
	KindSub    = "sub"    // 订阅拉取，detail 为 clash / v2ray
	KindLink   = "link"   // 来源直链
	KindBeacon = "beacon" // 实测回报
	KindLease  = "lease"  // 席位保活
	KindAPI    = "api"    // 网页接口
	KindWeb    = "web"    // 页面与静态资源
	KindHealth = "health" // 容器健康检查，只走本机回环
	KindProbe  = "probe"  // 延迟检测，detail 为节点协议
	KindExit   = "exit"   // 出口国家查询，detail 为节点协议
	KindSource = "source" // 来源刷新
	KindUsage  = "usage"  // 套餐用量读取，detail 为 head / get
	KindTLS    = "tls"    // 证书指纹读取，detail 为 tcp / quic
	KindMySQL  = "mysql"  // 数据库连接
	KindNIC    = "nic"    // 容器网卡计数，detail 为网卡名
)

type Key struct{ Kind, Detail string }

// Counter 单个分类的累计值，全部字段原子更新
type Counter struct {
	requests, failures, in, out atomic.Int64
}

func (c *Counter) Hit(failed bool) {
	if c == nil {
		return
	}
	c.requests.Add(1)
	if failed {
		c.failures.Add(1)
	}
}
func (c *Counter) AddIn(n int64) {
	if c != nil && n > 0 {
		c.in.Add(n)
	}
}
func (c *Counter) AddOut(n int64) {
	if c != nil && n > 0 {
		c.out.Add(n)
	}
}

type Values struct {
	Requests, Failures, In, Out int64
}

func (v Values) Zero() bool { return v.Requests == 0 && v.Failures == 0 && v.In == 0 && v.Out == 0 }

type Row struct {
	Key
	Values
}

type Meter struct {
	mu sync.RWMutex
	m  map[Key]*Counter
}

// Default 进程内唯一的计数器
var Default = &Meter{}

func (m *Meter) Counter(kind, detail string) *Counter {
	k := Key{kind, detail}
	m.mu.RLock()
	c := m.m[k]
	m.mu.RUnlock()
	if c != nil {
		return c
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[Key]*Counter{}
	}
	if c = m.m[k]; c == nil {
		c = &Counter{}
		m.m[k] = c
	}
	return c
}

// Drain 取出并清零全部增量，写库失败时用 Restore 放回
func (m *Meter) Drain() []Row { return m.rows(true) }

// Peek 读取尚未写库的增量
func (m *Meter) Peek() []Row { return m.rows(false) }

func (m *Meter) rows(reset bool) []Row {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Row, 0, len(m.m))
	for k, c := range m.m {
		var v Values
		if reset {
			v = Values{c.requests.Swap(0), c.failures.Swap(0), c.in.Swap(0), c.out.Swap(0)}
		} else {
			v = Values{c.requests.Load(), c.failures.Load(), c.in.Load(), c.out.Load()}
		}
		if !v.Zero() {
			out = append(out, Row{k, v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

func (m *Meter) Restore(rows []Row) {
	for _, r := range rows {
		c := m.Counter(r.Kind, r.Detail)
		c.requests.Add(r.Requests)
		c.failures.Add(r.Failures)
		c.in.Add(r.In)
		c.out.Add(r.Out)
	}
}
