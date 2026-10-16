package pool

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type Service struct {
	Store  *Store
	Prober Prober
	Client *http.Client
}

func NewService(store *Store, prober Prober) *Service {
	return &Service{Store: store, Prober: prober, Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("重定向次数过多")
		}
		if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
			return errors.New("不支持的重定向协议")
		}
		return nil
	}}}
}
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.sourceLoop(ctx) }()
	go func() { defer wg.Done(); s.nodeLoop(ctx) }()
	wg.Wait()
}
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (s *Service) ready(ctx context.Context) bool {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.Store.Ensure(c) == nil
}
func (s *Service) sourceLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if !s.ready(ctx) {
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		settings, e := s.Store.Settings(ctx)
		if e != nil {
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		var wg sync.WaitGroup
		for i := 0; i < settings.SourceConcurrency; i++ {
			src, e := s.Store.ClaimSource(ctx)
			if errors.Is(e, sql.ErrNoRows) {
				break
			}
			if e != nil {
				log.Print("来源任务领取失败，将自动重试")
				break
			}
			wg.Add(1)
			go func(src Source) { defer wg.Done(); s.Refresh(ctx, src) }(src)
		}
		wg.Wait()
		if !wait(ctx, time.Second) {
			return
		}
	}
}
func (s *Service) Refresh(parent context.Context, src Source) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	body := src.Content
	fail := func(message string) {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if e := s.Store.FailSource(c, src, message); e != nil {
			log.Print("记录来源失败状态失败，将在租约过期后重试")
		}
	}
	if src.Kind == "url" && (!src.Cached || body == "") {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
		if e != nil {
			fail("订阅地址无效")
			return
		}
		req.Header.Set("User-Agent", "clash.meta")
		req.Header.Set("Accept", "text/yaml, application/yaml, application/json, text/plain, */*")
		resp, e := s.Client.Do(req)
		if e != nil {
			fail("订阅请求失败或超时，保留上次节点")
			return
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
		resp.Body.Close()
		if e != nil || resp.StatusCode != http.StatusOK || len(b) > 16<<20 {
			fail("订阅返回异常或超过 16 MiB，保留上次节点")
			return
		}
		body = string(b)
	}
	parsed, e := Parse(body, s.Prober.Validate)
	if e != nil {
		fail(e.Error() + "；保留上次节点")
		return
	}
	if e = s.Store.ApplySource(ctx, src, body, parsed); e != nil {
		fail("节点保存失败，保留上次节点")
	}
}
func (s *Service) nodeLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if !s.ready(ctx) {
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		settings, e := s.Store.Settings(ctx)
		if e != nil {
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		nodes := []Node{}
		for i := 0; i < settings.ProbeConcurrency; i++ {
			n, e := s.Store.ClaimNode(ctx)
			if errors.Is(e, sql.ErrNoRows) {
				break
			}
			if e != nil {
				log.Print("节点任务领取失败，将自动重试")
				break
			}
			nodes = append(nodes, n)
		}
		results := make([]ProbeResult, len(nodes))
		var wg sync.WaitGroup
		for i, n := range nodes {
			wg.Add(1)
			go func(i int, n Node) {
				defer wg.Done()
				jobCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
				defer cancel()
				exit := needsExitLookup(n, time.Now())
				results[i] = s.Prober.Probe(jobCtx, n, settings, exit)
			}(i, n)
		}
		wg.Wait()
		if ctx.Err() != nil {
			return
		}
		for i, n := range nodes {
			saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if e := s.Store.SaveProbe(saveCtx, n, results[i]); e != nil {
				log.Print("节点检测结果保存失败，将在租约过期后重试")
			}
			cancel()
		}
		if !wait(ctx, time.Second) {
			return
		}
	}
}

func needsExitLookup(n Node, now time.Time) bool {
	return n.ExitCheckedAt == nil || now.Sub(*n.ExitCheckedAt) >= 24*time.Hour || n.ExitIP == "" || n.CountryCode == "" || (n.Region == "" && n.City == "")
}
