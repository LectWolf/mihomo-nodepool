package pool

import "testing"

func TestPickHomeOrder(t *testing.T) {
	limitedFull := nodeHome{SourceID: 1}
	limitedRoom := nodeHome{SourceID: 2, Room: true}
	held := nodeHome{SourceID: 3, Held: true}
	free := nodeHome{SourceID: 4, Free: true, Room: true}
	cases := []struct {
		name    string
		homes   []nodeHome
		current int64
		used    map[int64]bool
		want    int64
		ok      bool
	}{
		{"free first", []nodeHome{free, limitedRoom, held}, 2, nil, 4, true},
		{"held before new seat", []nodeHome{limitedRoom, held}, 2, nil, 3, true},
		{"used by other subscription", []nodeHome{limitedRoom, held}, 0, map[int64]bool{2: true}, 2, true},
		{"skip full source", []nodeHome{limitedFull, limitedRoom}, 1, nil, 2, true},
		{"keep current with room", []nodeHome{limitedRoom, {SourceID: 5, Room: true}}, 5, nil, 5, true},
		{"nothing available", []nodeHome{limitedFull}, 1, nil, 0, false},
		{"no homes", nil, 1, nil, 0, false},
	}
	for _, c := range cases {
		got, ok := pickHome(c.homes, c.current, c.used)
		if ok != c.ok || got.SourceID != c.want {
			t.Fatalf("%s: got %d %v", c.name, got.SourceID, ok)
		}
	}
}
