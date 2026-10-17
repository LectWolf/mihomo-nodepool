package core

import "testing"

func TestLocationProviderFormats(t *testing.T) {
	cases := []struct {
		name, body string
		want       exitLocation
	}{
		{"ip-api", `{"status":"success","query":"203.0.113.1","country":"美国","countryCode":"US","regionName":"加州","city":"洛杉矶"}`, exitLocation{"203.0.113.1", "美国", "US", "加州", "洛杉矶"}},
		{"ip-sb", `{"ip":"203.0.113.2","country":"United States","country_code":"us","region":"California","city":"Los Angeles"}`, exitLocation{"203.0.113.2", "United States", "US", "California", "Los Angeles"}},
		{"ipwho", `{"success":true,"ip":"2001:db8::1","country":"Japan","country_code":"JP","region":"Tokyo","city":"Shinjuku"}`, exitLocation{"2001:db8::1", "Japan", "JP", "Tokyo", "Shinjuku"}},
		{"partial", `{"ip":"203.0.113.3","country":" Singapore ","country_code":"sg"}`, exitLocation{"203.0.113.3", "Singapore", "SG", "", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLocation([]byte(tc.body))
			if err != nil || got != tc.want {
				t.Fatalf("location = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
	for _, body := range []string{`{"status":"fail","query":"203.0.113.1"}`, `{"success":false,"ip":"203.0.113.1"}`, `{"ip":"invalid"}`, `{}`} {
		if _, err := parseLocation([]byte(body)); err == nil {
			t.Fatalf("accepted failed lookup: %s", body)
		}
	}
}
