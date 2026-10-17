package pool

import "testing"

func TestMaskServersHidesForGuests(t *testing.T) {
	items := []Node{{Server: "a.example", Port: 443, Sources: []Origin{{OwnerID: 0}}}, {Server: "b.example", Port: 443, Sources: []Origin{{OwnerID: 7}}}}
	maskServers(GuestUser, items)
	for _, n := range items {
		if n.Server != "" || n.Port != 0 || !n.ServerHidden {
			t.Fatalf("guest must not see servers: %+v", n)
		}
	}
	own := []Node{{Server: "b.example", Port: 443, Sources: []Origin{{OwnerID: 7}}}}
	maskServers(User{ID: 7, Role: "user"}, own)
	if own[0].Server == "" {
		t.Fatal("owner should see own server")
	}
}
