package auth

import (
	"strconv"
	"sync"
	"time"
)

// loginRule 描述一个计数窗口：窗口内失败达到 limit 次即拒绝。
type loginRule struct {
	limit  int
	window time.Duration
}

// 登录限流按 IP 与「IP + 账号」两个维度计数，没有任何一个维度能锁住全站。
// 之前是进程级单一计数器，任意 10 次失败就能让所有人一分钟内无法登录。
var (
	loginIPRule   = loginRule{limit: 30, window: 10 * time.Minute}
	loginPairRule = loginRule{limit: 8, window: 15 * time.Minute}
)

type attemptWindow struct {
	failures int
	start    time.Time
}

// loginLimiter 记录失败次数并在窗口结束后自动失效。
type loginLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	items map[string]*attemptWindow
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{now: time.Now, items: map[string]*attemptWindow{}}
}

// retryAfter 返回任一维度被限流时还需要等待的时长；未限流返回 0。
func (l *loginLimiter) retryAfter(keys []string, rules []loginRule) time.Duration {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	var wait time.Duration
	for i, key := range keys {
		w := l.items[key]
		if w == nil || w.failures < rules[i].limit {
			continue
		}
		remain := rules[i].window - now.Sub(w.start)
		if remain > wait {
			wait = remain
		}
	}
	return wait
}

// fail 记录一次失败；窗口已过期就重新开始计数。
func (l *loginLimiter) fail(keys []string, rules []loginRule) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, key := range keys {
		w := l.items[key]
		if w == nil || now.Sub(w.start) >= rules[i].window {
			w = &attemptWindow{start: now}
			l.items[key] = w
		}
		w.failures++
	}
	if len(l.items) > 20000 {
		l.pruneLocked(now)
	}
}

// clear 在登录成功后清掉该组合的失败记录，正常用户不会因为手误被持续限制。
func (l *loginLimiter) clear(keys []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		delete(l.items, key)
	}
}

func (l *loginLimiter) pruneLocked(now time.Time) {
	for key, w := range l.items {
		if now.Sub(w.start) >= loginPairRule.window {
			delete(l.items, key)
		}
	}
}

// retryAfterHeader 把等待时长转成 Retry-After 需要的整秒。
func retryAfterHeader(wait time.Duration) string {
	seconds := int(wait / time.Second)
	if wait%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
