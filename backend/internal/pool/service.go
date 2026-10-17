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
	Store   *Store
	Prober  Prober
	Client  *http.Client
	metrics serviceMetrics
}

func NewService(store *Store, prober Prober) *Service {
	return &Service{Store: store, Prober: prober, metrics: serviceMetrics{startedAt: utcNow()}, Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
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
	wg.Add(3)
	go func() { defer wg.Done(); s.sourceLoop(ctx) }()
	go func() { defer wg.Done(); s.nodeLoop(ctx) }()
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			if s.ready(ctx) {
				if err := s.Store.SweepCommunity(ctx); err != nil {
					log.Print("订阅席位维护失败，将自动重试")
				}
				if err := s.Store.AwardDaily(ctx); err != nil {
					log.Print("每日共享奖励发放失败，将自动重试")
				}
				if err := s.hourly(ctx); err != nil {
					log.Print("每小时流量快照失败，将在下个整点重试")
				}
			}
			if !wait(ctx, time.Minute) {
				return
			}
		}
	}()
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
	s.metrics.sourceRuns.Add(1)
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	body := src.Content
	fail := func(message string) {
		s.metrics.sourceErrors.Add(1)
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
		s.metrics.sourceBytes.Add(int64(len(b)))
		if e != nil || resp.StatusCode != http.StatusOK || len(b) > 16<<20 {
			fail("订阅返回异常或超过 16 MiB，保留上次节点")
			return
		}
		body = string(b)
		src.SubscriptionUsage = ParseSubscriptionUsage(resp.Header.Get("Subscription-Userinfo"))
		if src.SubscriptionUsage != nil {
			fetched := utcNow()
			src.SubscriptionUsage.FetchedAt = &fetched
		}
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

// nodeLoop 维持持续的检测工作池：任一节点完成即领取下一个，不再整批等待最慢节点
func (s *Service) nodeLoop(ctx context.Context) {
	done := make(chan struct{}, 64) // 并发上限为 64，缓冲足够，退出时不会阻塞
	active := 0
	var wg sync.WaitGroup
	defer wg.Wait()
	for ctx.Err() == nil {
		idle := 5 * time.Second
		if s.ready(ctx) {
			if settings, e := s.Store.Settings(ctx); e == nil {
				idle = 2 * time.Second
				for active < settings.ProbeConcurrency {
					n, e := s.Store.ClaimNode(ctx)
					if e != nil {
						if !errors.Is(e, sql.ErrNoRows) && ctx.Err() == nil {
							log.Print("节点任务领取失败，将自动重试")
						}
						break
					}
					active++
					wg.Add(1)
					go func(n Node) {
						defer wg.Done()
						s.probeOne(ctx, n, settings)
						done <- struct{}{}
					}(n)
				}
			}
		}
		timer := time.NewTimer(idle)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-done:
			timer.Stop()
			active--
		drain:
			for {
				select {
				case <-done:
					active--
				default:
					break drain
				}
			}
		case <-timer.C:
		}
	}
}

func (s *Service) probeOne(ctx context.Context, n Node, settings Settings) {
	jobCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	result := s.Prober.Probe(jobCtx, n, settings, needsExitLookup(n, time.Now()))
	s.metrics.probeRuns.Add(1)
	if result.LatencyMS == nil {
		s.metrics.probeErrors.Add(1)
	}
	if ctx.Err() != nil {
		return
	}
	saveCtx, cancelSave := context.WithTimeout(ctx, 5*time.Second)
	defer cancelSave()
	if e := s.Store.SaveProbe(saveCtx, n, result); e != nil {
		log.Print("节点检测结果保存失败，将在租约过期后重试")
	}
}

func needsExitLookup(n Node, now time.Time) bool {
	return n.ExitCheckedAt == nil || now.Sub(*n.ExitCheckedAt) >= 24*time.Hour || n.ExitIP == "" || n.CountryCode == "" || (n.Region == "" && n.City == "")
}
