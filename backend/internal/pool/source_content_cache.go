package pool

import (
	"sync"
	"time"
)

const (
	sourceContentTTL = 60 * time.Second
	sourceContentMax = 64 << 20 // 64 MiB
)

type sourceContentEntry struct {
	content string
	at      time.Time
}

// sourceContentCache 缓存已解密的来源内容，避免每次订阅渲染都重复读取和 unseal。
// 来源刷新会调用 clear，另有 TTL 兜底。
type sourceContentCache struct {
	mu      sync.Mutex
	entries map[int64]sourceContentEntry
	total   int64
}

func (c *sourceContentCache) get(id int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok {
		return "", false
	}
	if time.Since(e.at) > sourceContentTTL {
		c.total -= int64(len(e.content))
		delete(c.entries, id)
		return "", false
	}
	return e.content, true
}

func (c *sourceContentCache) put(id int64, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[int64]sourceContentEntry{}
	}
	if old, ok := c.entries[id]; ok {
		c.total -= int64(len(old.content))
	}
	c.entries[id] = sourceContentEntry{content: content, at: time.Now()}
	c.total += int64(len(content))
	for c.total > sourceContentMax && len(c.entries) > 1 {
		var oldestID int64
		var oldest time.Time
		first := true
		for id, e := range c.entries {
			if first || e.at.Before(oldest) {
				oldestID, oldest, first = id, e.at, false
			}
		}
		if first {
			break
		}
		c.total -= int64(len(c.entries[oldestID].content))
		delete(c.entries, oldestID)
	}
}

func (c *sourceContentCache) clear() {
	c.mu.Lock()
	c.entries = nil
	c.total = 0
	c.mu.Unlock()
}
