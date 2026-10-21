package pool

import (
	"testing"
	"time"
)

func TestEnvDuration(t *testing.T) {
	const key = "NODEPOOL_TEST_CACHE_TTL"
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"未设置", "", 5 * time.Minute},
		{"合法值", "90s", 90 * time.Second},
		{"非法值", "十分钟", 5 * time.Minute},
		{"小于下限", "5s", 5 * time.Minute},
		{"大于上限", "3h", 5 * time.Minute},
		{"前后空格", "  2m  ", 2 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(key, c.raw)
			if got := envDuration(key, 5*time.Minute, 30*time.Second, time.Hour); got != c.want {
				t.Fatalf("envDuration(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestSameLinks(t *testing.T) {
	base := map[int64]string{1: "HK-01", 2: "JP-02"}
	cases := []struct {
		name  string
		after map[int64]string
		want  bool
	}{
		{"完全相同", map[int64]string{1: "HK-01", 2: "JP-02"}, true},
		{"键顺序无关", map[int64]string{2: "JP-02", 1: "HK-01"}, true},
		{"新增节点", map[int64]string{1: "HK-01", 2: "JP-02", 3: "SG-03"}, false},
		{"删除节点", map[int64]string{1: "HK-01"}, false},
		{"改名", map[int64]string{1: "HK-01", 2: "JP-02 备用"}, false},
		{"换节点", map[int64]string{1: "HK-01", 9: "JP-02"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameLinks(base, c.after); got != c.want {
				t.Fatalf("sameLinks = %v, want %v", got, c.want)
			}
		})
	}
	if !sameLinks(map[int64]string{}, nil) {
		t.Fatal("两个空集合应视为相同")
	}
}
