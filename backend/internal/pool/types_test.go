package pool

import (
	"testing"
	"time"
)

func TestRetryDelay(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, time.Hour},
		{1, time.Hour},
		{2, 2 * time.Hour},
		{3, 4 * time.Hour},
		{4, 8 * time.Hour},
		{5, 16 * time.Hour},
		{6, 24 * time.Hour},
		{20, 24 * time.Hour},
	}
	for _, tc := range cases {
		if got := RetryDelay(tc.failures); got != tc.want {
			t.Errorf("RetryDelay(%d) = %s, want %s", tc.failures, got, tc.want)
		}
	}
}
