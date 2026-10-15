package core

import (
	"context"
	"time"
)

// Engine is the boundary for the future embedded Mihomo integration.
// No engine is wired into the preparation build.
type Engine interface {
	Version() string
	DelayTest(ctx context.Context, nodeName, testURL string, timeout time.Duration) (time.Duration, error)
	Close() error
}
