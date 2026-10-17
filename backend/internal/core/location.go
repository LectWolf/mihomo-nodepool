package core

import (
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
)

type exitLocation struct {
	IP, Country, CountryCode, Region, City string
}

// Normalize the reference project's localized ip-api response and the two
// fallback providers. Every response is obtained through the node's adapter.
func parseLocation(body []byte) (exitLocation, error) {
	var raw struct {
		IP          string `json:"ip"`
		Query       string `json:"query"`
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
		Code        string `json:"countryCode"`
		Region      string `json:"region"`
		RegionName  string `json:"regionName"`
		City        string `json:"city"`
		Success     *bool  `json:"success"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return exitLocation{}, err
	}
	if (raw.Success != nil && !*raw.Success) || (raw.Status != "" && raw.Status != "success") {
		return exitLocation{}, errors.New("location provider rejected lookup")
	}
	ip := strings.TrimSpace(raw.IP)
	if ip == "" {
		ip = strings.TrimSpace(raw.Query)
	}
	parsed, err := netip.ParseAddr(ip)
	if err != nil {
		return exitLocation{}, errors.New("location provider returned invalid IP")
	}
	code := strings.ToUpper(strings.TrimSpace(raw.CountryCode))
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(raw.Code))
	}
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		code = ""
	}
	region := strings.TrimSpace(raw.RegionName)
	if region == "" {
		region = strings.TrimSpace(raw.Region)
	}
	return exitLocation{
		IP: parsed.Unmap().String(), Country: strings.TrimSpace(raw.Country),
		CountryCode: code, Region: region, City: strings.TrimSpace(raw.City),
	}, nil
}
