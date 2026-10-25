package auth

import (
	"testing"
	"time"
)

func TestLoginLimiterSeparatesIPAndPair(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Unix(1700000000, 0)
	limiter.now = func() time.Time { return now }
	rules := []loginRule{loginIPRule, loginPairRule}

	ipA := []string{"ip:1.1.1.1", "pair:1.1.1.1\x00admin"}
	ipB := []string{"ip:2.2.2.2", "pair:2.2.2.2\x00admin"}

	for i := 0; i < loginPairRule.limit; i++ {
		limiter.fail(ipA, rules)
	}
	if wait := limiter.retryAfter(ipA, rules); wait <= 0 {
		t.Fatal("pair limit not reached")
	}
	// 同一账号在另一个 IP 上不受影响：不会出现全站或跨 IP 的锁死。
	if wait := limiter.retryAfter(ipB, rules); wait != 0 {
		t.Fatalf("other IP blocked for %s", wait)
	}
	// 同一 IP 上的其他账号在 IP 计数未满时仍可登录。
	other := []string{"ip:1.1.1.1", "pair:1.1.1.1\x00bob"}
	if wait := limiter.retryAfter(other, rules); wait != 0 {
		t.Fatalf("other account blocked for %s", wait)
	}
}

func TestLoginLimiterIPCeiling(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Unix(1700000000, 0)
	limiter.now = func() time.Time { return now }
	rules := []loginRule{loginIPRule, loginPairRule}

	for i := 0; i < loginIPRule.limit; i++ {
		keys := []string{"ip:9.9.9.9", "pair:9.9.9.9\x00user" + string(rune('a'+i%26))}
		limiter.fail(keys, rules)
	}
	if wait := limiter.retryAfter([]string{"ip:9.9.9.9", "pair:9.9.9.9\x00zzz"}, rules); wait <= 0 {
		t.Fatal("IP limit not reached")
	}
	if wait := limiter.retryAfter([]string{"ip:8.8.8.8", "pair:8.8.8.8\x00admin"}, rules); wait != 0 {
		t.Fatal("other IP blocked")
	}
}

func TestLoginLimiterExpiresAndClears(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Unix(1700000000, 0)
	limiter.now = func() time.Time { return now }
	rules := []loginRule{loginIPRule, loginPairRule}
	keys := []string{"ip:3.3.3.3", "pair:3.3.3.3\x00admin"}
	for i := 0; i < loginPairRule.limit; i++ {
		limiter.fail(keys, rules)
	}
	if limiter.retryAfter(keys, rules) <= 0 {
		t.Fatal("expected block")
	}
	limiter.clear(keys[1:])
	if limiter.retryAfter(keys, rules) != 0 {
		t.Fatal("clear did not reset pair")
	}
	for i := 0; i < loginPairRule.limit; i++ {
		limiter.fail(keys, rules)
	}
	now = now.Add(loginPairRule.window + time.Second)
	if limiter.retryAfter(keys, rules) != 0 {
		t.Fatal("window expiry did not reset")
	}
}
