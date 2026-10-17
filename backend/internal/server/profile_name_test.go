package server

import (
	"encoding/base64"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestSetProfileName(t *testing.T) {
	w := httptest.NewRecorder()
	setProfileName(w)
	cd := w.Header().Get("Content-Disposition")
	i := strings.Index(cd, "filename*=UTF-8''")
	if i < 0 {
		t.Fatalf("missing filename*: %q", cd)
	}
	name, err := url.PathUnescape(cd[i+len("filename*=UTF-8''"):])
	if err != nil || name != pool.SubscriptionDisplayName {
		t.Fatalf("filename* %q %v", name, err)
	}
	title := strings.TrimPrefix(w.Header().Get("Profile-Title"), "base64:")
	decoded, err := base64.StdEncoding.DecodeString(title)
	if err != nil || string(decoded) != pool.SubscriptionDisplayName {
		t.Fatalf("profile title %q %v", decoded, err)
	}
}
