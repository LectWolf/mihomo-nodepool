package pool

import (
	"testing"
	"time"
)

func TestExitLookupRefreshesMissingLocationAndUsesCache(t *testing.T) {
	now := time.Now()
	recent := now.Add(-time.Hour)
	old := now.Add(-24 * time.Hour)
	complete := Node{ExitIP: "203.0.113.1", CountryCode: "US", Region: "加州", City: "洛杉矶", ExitCheckedAt: &recent}
	if needsExitLookup(complete, now) {
		t.Fatal("complete recent location must use cache")
	}
	legacy := complete
	legacy.Region, legacy.City = "", ""
	if !needsExitLookup(legacy, now) {
		t.Fatal("old country-only rows must fetch region and city on next probe")
	}
	complete.ExitCheckedAt = &old
	if !needsExitLookup(complete, now) {
		t.Fatal("location cache must expire after 24 hours")
	}
	if Period != 8*time.Hour {
		t.Fatal("refresh period must be eight hours")
	}
}
