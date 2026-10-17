package cnip

import "testing"

func TestContains(t *testing.T) {
	for ip, want := range map[string]bool{
		"114.114.114.114": true, "223.5.5.5": true, "1.0.1.1": true, "::ffff:114.114.114.114": true,
		"240e::1": true, "8.8.8.8": false, "1.1.1.1": false, "203.0.113.9": false, "2001:4860::8888": false, "bad": false,
	} {
		if Contains(ip) != want {
			t.Fatalf("%s: want %v", ip, want)
		}
	}
}
