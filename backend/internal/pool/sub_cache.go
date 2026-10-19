package pool

import (
	"sync"
	"time"
)

const (
	// SubscriptionCacheTTL 是订阅渲染结果的短缓存时间。
	// 来源/用户结构变化会通过 InvalidateIndex 立即清空。
	SubscriptionCacheTTL = 60 * time.Second
	subscriptionCacheMax = 64 << 20 // 64 MiB
)

// SubscriptionCacheEntry 是一次订阅渲染的完整结果。
type SubscriptionCacheEntry struct {
	Body           []byte
	ETag           string
	State          string
	Format         string
	Skipped        int
	RoutingSkipped int
}

type subscriptionCacheEntry struct {
	SubscriptionCacheEntry
	at time.Time
}

type subscriptionCache struct {
	mu      sync.Mutex
	entries map[string]subscriptionCacheEntry
	total   int64
}

func (c *subscriptionCache) get(key string) (SubscriptionCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return SubscriptionCacheEntry{}, false
	}
	if time.Since(e.at) > SubscriptionCacheTTL {
		c.total -= int64(len(e.Body))
		delete(c.entries, key)
		return SubscriptionCacheEntry{}, false
	}
	return e.SubscriptionCacheEntry, true
}

func (c *subscriptionCache) put(key string, v SubscriptionCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]subscriptionCacheEntry{}
	}
	if old, ok := c.entries[key]; ok {
		c.total -= int64(len(old.Body))
	}
	c.entries[key] = subscriptionCacheEntry{SubscriptionCacheEntry: v, at: time.Now()}
	c.total += int64(len(v.Body))
	for c.total > subscriptionCacheMax && len(c.entries) > 1 {
		var oldestKey string
		var oldest time.Time
		first := true
		for key, e := range c.entries {
			if first || e.at.Before(oldest) {
				oldestKey, oldest, first = key, e.at, false
			}
		}
		if first {
			break
		}
		c.total -= int64(len(c.entries[oldestKey].Body))
		delete(c.entries, oldestKey)
	}
}

func (c *subscriptionCache) clear() {
	c.mu.Lock()
	c.entries = nil
	c.total = 0
	c.mu.Unlock()
}

// CachedSubscription 读取订阅渲染缓存。
func (s *Store) CachedSubscription(key string) (SubscriptionCacheEntry, bool) {
	return s.subs.get(key)
}

// CacheSubscription 写入订阅渲染缓存。
func (s *Store) CacheSubscription(key string, v SubscriptionCacheEntry) {
	s.subs.put(key, v)
}

// ClearSubscriptionCache 清空订阅渲染缓存。
func (s *Store) ClearSubscriptionCache() {
	s.subs.clear()
}
