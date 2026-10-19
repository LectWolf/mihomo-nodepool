package traffic

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func find(rows []Row, kind, detail string) Values {
	for _, r := range rows {
		if r.Kind == kind && r.Detail == detail {
			return r.Values
		}
	}
	return Values{}
}

func TestServerAttribution(t *testing.T) {
	m := &Meter{}
	mux := http.NewServeMux()
	mux.HandleFunc("/sub/x", func(w http.ResponseWriter, r *http.Request) {
		TagRequest(r, KindSub, "clash")
		_, _ = w.Write([]byte(strings.Repeat("a", 50000)))
	})
	mux.HandleFunc("/api/v1/x", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := &http.Server{Handler: Classify(m, mux)}
	Server(srv)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(Listen(ln, m))
	defer srv.Close()
	client := &http.Client{}
	for _, p := range []string{"/sub/x", "/api/v1/x", "/sub/x"} {
		resp, err := client.Get("http://" + ln.Addr().String() + p)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	time.Sleep(100 * time.Millisecond)
	rows := m.Drain()
	sub := find(rows, KindSub, "clash")
	if sub.Requests != 2 || sub.Out < 100000 || sub.In == 0 {
		t.Fatalf("sub = %+v, rows %+v", sub, rows)
	}
	if v := find(rows, KindSub, ""); v.Requests != 0 {
		t.Fatalf("retag left %+v", v)
	}
	api := find(rows, KindAPI, "")
	if api.Requests != 1 || api.Out == 0 || api.Out > 1000 {
		t.Fatalf("api = %+v", api)
	}
	if len(m.Drain()) != 0 {
		t.Fatal("drain should reset")
	}
	m.Restore(rows)
	if find(m.Peek(), KindSub, "clash").Out != sub.Out {
		t.Fatal("restore failed")
	}
}

func TestNICSample(t *testing.T) {
	dir := t.TempDir()
	NICPath = filepath.Join(dir, "dev")
	defer func() { NICPath = "/proc/net/dev" }()
	write := func(rx, tx string) {
		body := "Inter-|   Receive\n face |bytes packets errs drop fifo frame compressed multicast|bytes\n    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n  eth0: " + rx + " 10 0 0 0 0 0 0 " + tx + " 10 0 0 0 0 0 0\n"
		if err := os.WriteFile(NICPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := &Meter{}
	var s NICSampler
	write("1000", "2000")
	s.Sample(m)
	write("1500", "2600")
	s.Sample(m)
	v := find(m.Drain(), KindNIC, "eth0")
	if v.In != 500 || v.Out != 600 {
		t.Fatalf("nic = %+v", v)
	}
}
