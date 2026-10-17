package pool

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSealRoundTripAndPurposeBinding(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := SetDataKey(key); err != nil {
		t.Fatal(err)
	}
	defer SetDataKey("")
	v, err := seal(purposeSourceURL, "https://example.com/sub?token=1")
	if err != nil || !strings.HasPrefix(v, sealPrefix) {
		t.Fatalf("seal %q %v", v, err)
	}
	if again, _ := seal(purposeSourceURL, v); again != v {
		t.Fatal("double seal")
	}
	plain, err := unseal(purposeSourceURL, v)
	if err != nil || plain != "https://example.com/sub?token=1" {
		t.Fatalf("unseal %q %v", plain, err)
	}
	if _, err = unseal(purposeNodeServer, v); err == nil {
		t.Fatal("ciphertext accepted for another purpose")
	}
	cfg, err := sealConfig([]byte(`{"name":"a","server":"1.2.3.4"}`))
	if err != nil || cfg[0] != '"' || strings.Contains(string(cfg), "1.2.3.4") {
		t.Fatalf("config %s %v", cfg, err)
	}
	back, err := unsealConfig(cfg)
	if err != nil || string(back) != `{"name":"a","server":"1.2.3.4"}` {
		t.Fatalf("config back %s %v", back, err)
	}
	if err = SetDataKey(strings.Repeat("ab", 32)); err != nil {
		t.Fatal("hex key", err)
	}
	if _, err = unseal(purposeSourceURL, v); err == nil {
		t.Fatal("wrong key accepted")
	}
	if err = SetDataKey("short"); err == nil {
		t.Fatal("short key accepted")
	}
	SetDataKey("")
	if out, _ := seal(purposeSourceURL, "plain"); out != "plain" {
		t.Fatal("sealed without key")
	}
	if _, err = unseal(purposeSourceURL, v); err != ErrDataKey {
		t.Fatal("missing key not reported", err)
	}
}

func TestCommunityDefaultsAndLimits(t *testing.T) {
	c := DefaultCommunity()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	u := User{XP: 0, NodeLimit: 150, SourceLimit: 5, SubscriptionLimit: 1}
	u.Level = LevelForXP(u.XP)
	l := composeLimits(u, c, LevelQuota{Nodes: 100})
	if l.Nodes != 300 || l.Sources != 5 || l.Subscriptions != 1 {
		t.Fatalf("limits %+v", l)
	}
	u.XP = 30000
	u.Level = LevelForXP(u.XP)
	if l = composeLimits(u, c, LevelQuota{}); l.Nodes != 1000 || l.Sources != 10 || l.Subscriptions != 5 {
		t.Fatalf("top level %+v", l)
	}
	c.ShareQualityHalf = 90
	if c.Validate() == nil {
		t.Fatal("half above full accepted")
	}
	c = DefaultCommunity()
	c.Titles = append(c.Titles, TitleItem{Name: "共享先锋", Price: 1})
	if c.Validate() == nil {
		t.Fatal("duplicate title accepted")
	}
}
