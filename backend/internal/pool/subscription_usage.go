package pool

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// SubscriptionUsage contains only parsed subscription-userinfo metadata. Nil
// numbers mean unknown, never unlimited. TotalBytes == 0 is also an unknown
// quota, so RemainingBytes stays nil. FetchedAt is set by the fetch caller.
//
// Status is one of unknown, normal, low, exhausted, expired, invalid. A known
// expiry at or before now wins; otherwise malformed/duplicate recognized fields
// or an overflowing usage sum are invalid. With all three byte counters known
// and a positive quota, zero remaining is exhausted, <=10% is low, and the rest
// is normal. Incomplete counters or a zero quota remain unknown, even when the
// known expiry is in the future. Valid independent fields survive invalid ones.
// No source identifiers, URLs, credentials or raw header values are retained.
type SubscriptionUsage struct {
	UploadBytes    *int64     `json:"upload_bytes"`
	DownloadBytes  *int64     `json:"download_bytes"`
	TotalBytes     *int64     `json:"total_bytes"`
	RemainingBytes *int64     `json:"remaining_bytes"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Status         string     `json:"status"`
	FetchedAt      *time.Time `json:"fetched_at"`
}

// ParseSubscriptionUsage uses the current UTC time. Empty headers and headers
// containing only unrecognized fields return nil (metadata was not provided).
func ParseSubscriptionUsage(raw string) *SubscriptionUsage {
	return ParseSubscriptionUsageAt(raw, utcNow())
}

// ParseSubscriptionUsageAt parses nonnegative decimal int64 bytes without a
// floating-point conversion. Expiry must be Unix seconds in [2000-01-01,
// 2101-01-01); this explicit subscription-date range rejects implausible epochs,
// including millisecond timestamps, rather than silently misinterpreting them.
func ParseSubscriptionUsageAt(raw string, now time.Time) *SubscriptionUsage {
	u := &SubscriptionUsage{Status: "unknown"}
	seen := map[string]bool{}
	invalid := false
	for _, item := range strings.Split(raw, ";") {
		key, value, hasValue := strings.Cut(strings.TrimSpace(item), "=")
		key = strings.ToLower(strings.TrimSpace(key))
		switch key {
		case "upload", "download", "total", "expire":
		default:
			continue
		}
		duplicate := seen[key]
		seen[key] = true
		value = strings.TrimSpace(value)
		n, err := strconv.ParseInt(value, 10, 64)
		// ParseInt accepts a leading + or -, but the header uses decimal digits.
		valid := hasValue && value != "" && err == nil && n >= 0
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				valid = false
				break
			}
		}
		if key == "expire" && (n < 946684800 || n >= 4133980800) {
			valid = false
		}
		if duplicate || !valid {
			invalid = true
			// A conflicting duplicate makes that individual field unknown too.
			switch key {
			case "upload":
				u.UploadBytes = nil
			case "download":
				u.DownloadBytes = nil
			case "total":
				u.TotalBytes = nil
			case "expire":
				u.ExpiresAt = nil
			}
			continue
		}
		switch key {
		case "upload":
			u.UploadBytes = &n
		case "download":
			u.DownloadBytes = &n
		case "total":
			u.TotalBytes = &n
		case "expire":
			expiry := time.Unix(n, 0).UTC()
			u.ExpiresAt = &expiry
		}
	}
	if len(seen) == 0 {
		return nil
	}
	if u.UploadBytes != nil && u.DownloadBytes != nil {
		if *u.UploadBytes > math.MaxInt64-*u.DownloadBytes {
			invalid = true
		} else if u.TotalBytes != nil && *u.TotalBytes > 0 {
			remaining := *u.TotalBytes - (*u.UploadBytes + *u.DownloadBytes)
			if remaining < 0 {
				remaining = 0
			}
			u.RemainingBytes = &remaining
		}
	}
	switch {
	case u.ExpiresAt != nil && !u.ExpiresAt.After(now):
		u.Status = "expired"
	case invalid:
		u.Status = "invalid"
	case u.RemainingBytes == nil:
		u.Status = "unknown"
	case *u.RemainingBytes == 0:
		u.Status = "exhausted"
	case *u.RemainingBytes <= *u.TotalBytes/10:
		// Integer division gives an exact <=10% test without multiplication
		// overflow or floating-point rounding near the int64 limit.
		u.Status = "low"
	default:
		u.Status = "normal"
	}
	return u
}

// Refresh the wall-clock part of cached metadata without fabricating a fetch.
func refreshUsageStatus(u *SubscriptionUsage) {
	if u != nil && u.ExpiresAt != nil && !u.ExpiresAt.After(utcNow()) {
		u.Status = "expired"
	}
}
