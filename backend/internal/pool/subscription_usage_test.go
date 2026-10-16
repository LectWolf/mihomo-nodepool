package pool

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionUsageStatuses(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, raw, status string
		remaining         *int64
	}{
		{"empty", "", "", nil},
		{"blank", " ; ;\t", "", nil},
		{"unknown", "account=private; reset=123", "", nil},
		{"normal", "upload=1;download=2;total=100", "normal", usageTestInt64(97)},
		{"case and whitespace", " Upload = 20 ; DOWNLOAD=70; ToTaL = 100; ignored=value;", "low", usageTestInt64(10)},
		{"above ten percent", "upload=0;download=89;total=100", "normal", usageTestInt64(11)},
		{"below ten percent", "upload=0;download=91;total=100", "low", usageTestInt64(9)},
		{"fractional ten percent", "upload=0;download=91;total=101", "low", usageTestInt64(10)},
		{"fractional above ten percent", "upload=0;download=90;total=101", "normal", usageTestInt64(11)},
		{"tiny positive quota", "upload=0;download=0;total=1", "normal", usageTestInt64(1)},
		{"exhausted", "upload=30;download=70;total=100", "exhausted", usageTestInt64(0)},
		{"over quota clamps", "upload=100;download=70;total=100", "exhausted", usageTestInt64(0)},
		{"zero quota", "upload=0;download=0;total=0", "unknown", nil},
		{"zero quota with usage", "upload=100;download=100;total=0", "unknown", nil},
		{"missing upload", "download=0;total=100", "unknown", nil},
		{"missing download", "upload=0;total=100", "unknown", nil},
		{"missing total", "upload=0;download=0", "unknown", nil},
		{"only total", "total=100", "unknown", nil},
		{"only upload", "upload=0", "unknown", nil},
		{"duplicate", "upload=0;UPLOAD=0;download=0;total=100", "invalid", nil},
		{"three duplicate values", "upload=1;upload=2;upload=3;download=0;total=100", "invalid", nil},
		{"sum overflow", "upload=9223372036854775807;download=1;total=9223372036854775807", "invalid", nil},
		{"sum overflow without quota", "upload=9223372036854775807;download=1", "invalid", nil},
		{"sum max exact", "upload=9223372036854775806;download=1;total=9223372036854775807", "exhausted", usageTestInt64(0)},
		{"max quota remains", "upload=0;download=0;total=9223372036854775807", "normal", usageTestInt64(math.MaxInt64)},
		{"max quota low boundary", "upload=8301034833169298227;download=0;total=9223372036854775807", "low", usageTestInt64(922337203685477580)},
		{"max quota above low boundary", "upload=8301034833169298226;download=0;total=9223372036854775807", "normal", usageTestInt64(922337203685477581)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSubscriptionUsageAt(tc.raw, now)
			if tc.status == "" {
				if got != nil {
					t.Fatalf("metadata not provided: got %+v", got)
				}
				return
			}
			if got == nil || got.Status != tc.status || !reflect.DeepEqual(got.RemainingBytes, tc.remaining) {
				t.Fatalf("got %+v, want status %s remaining %v", got, tc.status, tc.remaining)
			}
			if got.FetchedAt != nil {
				t.Fatal("parser must leave fetched_at to its caller")
			}
		})
	}
}

func TestSubscriptionUsageInvalidFields(t *testing.T) {
	for _, key := range []string{"upload", "download", "total", "expire"} {
		for _, value := range []string{"", "-1", "-0", "+1", "1.5", "1e3", "NaN", "unlimited", "9223372036854775808", "18446744073709551615", "1=2", "１２"} {
			t.Run(key+"="+value, func(t *testing.T) {
				got := ParseSubscriptionUsageAt(key+"="+value, time.Now())
				if got == nil || got.Status != "invalid" || got.UploadBytes != nil || got.DownloadBytes != nil || got.TotalBytes != nil || got.RemainingBytes != nil || got.ExpiresAt != nil {
					t.Fatalf("malformed field became known: %+v", got)
				}
			})
		}
		if got := ParseSubscriptionUsage(key); got == nil || got.Status != "invalid" {
			t.Fatalf("recognized key without equals: %+v", got)
		}
	}
	got := ParseSubscriptionUsage("upload=invalid;download=12;total=100")
	if got == nil || got.Status != "invalid" || got.UploadBytes != nil || got.DownloadBytes == nil || *got.DownloadBytes != 12 || got.TotalBytes == nil || *got.TotalBytes != 100 || got.RemainingBytes != nil {
		t.Fatalf("valid independent counters should survive: %+v", got)
	}
}

func TestSubscriptionUsageExpiry(t *testing.T) {
	now := time.Unix(1792152000, 0).UTC()
	for _, tc := range []struct {
		name, raw, status string
		expires           bool
	}{
		{"future only", "expire=1792152001", "unknown", true},
		{"past only", "expire=1792151999", "expired", true},
		{"exact now", "expire=1792152000", "expired", true},
		{"expired wins exhausted", "expire=1792151999;upload=100;download=0;total=100", "expired", true},
		{"expired wins invalid", "expire=1792151999;upload=-1", "expired", true},
		{"future normal", "EXPIRE=1792152001;upload=0;download=0;total=100", "normal", true},
		{"future exhausted", "expire=1792152001;upload=100;download=0;total=100", "exhausted", true},
		{"milliseconds", "expire=1792152000000", "invalid", false},
		{"zero is not forever", "expire=0", "invalid", false},
		{"implausibly old", "expire=1", "invalid", false},
		{"lower bound", "expire=946684800", "expired", true},
		{"below lower bound", "expire=946684799", "invalid", false},
		{"upper last second", "expire=4133980799", "unknown", true},
		{"upper bound excluded", "expire=4133980800", "invalid", false},
		{"duplicate expiry", "expire=1792151999;expire=1792152001", "invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSubscriptionUsageAt(tc.raw, now.In(time.FixedZone("offset", 8*3600)))
			if got == nil || got.Status != tc.status || (got.ExpiresAt != nil) != tc.expires {
				t.Fatalf("got %+v, want status %s expiry known %v", got, tc.status, tc.expires)
			}
			if got.ExpiresAt != nil && got.ExpiresAt.Location() != time.UTC {
				t.Fatal("expiry must be UTC")
			}
		})
	}
}

func TestSubscriptionUsageExactBytesAndSafeJSON(t *testing.T) {
	raw := "upload=9007199254740993;download=2;total=9007199254741095;secret=https://user:password@example.com/token"
	got := ParseSubscriptionUsage(raw)
	if got == nil || got.UploadBytes == nil || *got.UploadBytes != 9007199254740993 || got.DownloadBytes == nil || *got.DownloadBytes != 2 || got.TotalBytes == nil || *got.TotalBytes != 9007199254741095 || got.RemainingBytes == nil || *got.RemainingBytes != 100 {
		t.Fatalf("byte precision lost: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"upload_bytes":9007199254740993,"download_bytes":2,"total_bytes":9007199254741095,"remaining_bytes":100,"expires_at":null,"status":"low","fetched_at":null}`
	if string(encoded) != want || strings.Contains(string(encoded), "password") {
		t.Fatalf("unexpected JSON: %s", encoded)
	}
	unknown := ParseSubscriptionUsage("total=0")
	if unknown.TotalBytes == nil || *unknown.TotalBytes != 0 || unknown.UploadBytes != nil || unknown.DownloadBytes != nil || unknown.RemainingBytes != nil {
		t.Fatalf("zero and missing values must stay distinguishable: %+v", unknown)
	}
}

func usageTestInt64(n int64) *int64 { return &n }
