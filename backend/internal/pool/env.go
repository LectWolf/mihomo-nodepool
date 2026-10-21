package pool

import (
	"os"
	"strings"
	"time"
)

// envDuration 读取时长型环境变量；未设置、格式错误或超出 min–max 时返回 fallback。
// 缓存类参数用环境变量暴露，便于按机器规格调整而不用重新编译。
func envDuration(key string, fallback, min, max time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v < min || v > max {
		return fallback
	}
	return v
}
