package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo"
)

func TestNewUsesSafeDefaults(t *testing.T) {
	m, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.repo != defaultRepository || m.version != buildinfo.Version {
		t.Fatalf("repo=%s version=%s", m.repo, m.version)
	}
	if m.apiBase != githubAPI || m.restartWait != 500*time.Millisecond {
		t.Fatalf("base=%s delay=%s", m.apiBase, m.restartWait)
	}
	if m.cacheTTL < 30*time.Second || m.cacheTTL > 60*time.Second || m.goos != runtime.GOOS {
		t.Fatalf("ttl=%s os=%s", m.cacheTTL, m.goos)
	}
	if _, err := New(Options{Repository: "https://github.com/a/b"}); err == nil {
		t.Fatal("expected invalid repository")
	}
	opt := reflect.TypeOf(Options{})
	if opt.NumField() != 2 || opt.Field(0).Name != "Repository" || opt.Field(1).Name != "Version" {
		t.Fatal("Options exposes more than repository and version")
	}
	methods := reflect.TypeOf(&Manager{}).NumMethod()
	if methods != 1 || reflect.TypeOf(&Manager{}).Method(0).Name != "Register" {
		t.Fatal("Manager must only export Register")
	}
}

func TestVersionSortAndReleaseContract(t *testing.T) {
	if cmp, ok := compareVersions("v1.10.0", "v1.9.0"); !ok || cmp <= 0 {
		t.Fatalf("numeric compare = %d %v", cmp, ok)
	}
	srv, fake := newGitHub(t)
	fake.set(sampleReleases(srv.URL))
	exe := writeExe(t, "old-binary")
	m := newTestManager(t, srv, "v1.2.3", exe)
	rr := call(t, m, http.MethodGet, "/api/v1/releases", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d %s", rr.Code, rr.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"current_version", "latest_version", "update_available", "can_update", "can_rollback", "need_restart", "state", "message", "releases"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing %s in %s", key, rr.Body)
		}
	}
	var view statusView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.CurrentVersion != "v1.2.3" || view.LatestVersion != "v1.10.0" || !view.UpdateAvailable || !view.CanUpdate || !view.CanRollback {
		t.Fatalf("status %+v", view)
	}
	if view.State != stateIdle || view.NeedRestart || len(view.Releases) != 3 {
		t.Fatalf("status %+v", view)
	}
	if view.Releases[0].Version != "v1.10.0" || view.Releases[1].Version != "v1.9.0" || view.Releases[2].Version != "v1.2.0" {
		t.Fatalf("order %+v", view.Releases)
	}
	if view.Releases[0].Body != "ten notes" || view.Releases[0].Name != "Ten" || !strings.Contains(view.Releases[0].URL, "/tag/v1.10.0") {
		t.Fatalf("release content %+v", view.Releases[0])
	}
	if fake.hits() != 1 || !fake.saw("per_page=30") {
		t.Fatalf("hits=%d seen=%v", fake.hits(), fake.requests())
	}
	call(t, m, http.MethodGet, "/api/v1/releases", "")
	if fake.hits() != 1 {
		t.Fatalf("cache missed, hits=%d", fake.hits())
	}
}

func TestReleaseListErrorKeepsInstallState(t *testing.T) {
	token := "upd-token-9f3a"
	t.Setenv("UPDATE_GITHUB_TOKEN", token)
	block := make(chan struct{})
	srv, fake := newGitHub(t)
	fake.block = block
	fake.set(sampleReleases(srv.URL))
	exe := writeExe(t, "old-binary")
	m := newTestManager(t, srv, "v1.2.3", exe)
	m.cacheTTL = 0
	rr := call(t, m, http.MethodPost, "/api/v1/system/update", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"queued":true`) {
		t.Fatalf("queue %d %s", rr.Code, rr.Body)
	}
	waitState(t, m, stateDownloading)
	fake.setFail("list-failed " + token)
	got := call(t, m, http.MethodGet, "/api/v1/releases", "")
	if got.Code != http.StatusOK {
		t.Fatalf("get %d %s", got.Code, got.Body)
	}
	body := got.Body.String()
	if strings.Contains(body, token) || !strings.Contains(body, "list-failed") || !strings.Contains(body, "[redacted]") || !strings.Contains(body, `"state":"downloading"`) {
		t.Fatalf("body %s", body)
	}
	again := call(t, m, http.MethodPost, "/api/v1/system/update", "")
	if again.Code != http.StatusConflict {
		t.Fatalf("conflict %d %s", again.Code, again.Body)
	}
	close(block)
	snap := waitTerminal(t, m)
	if snap.state != stateDone || !snap.needRestart || !strings.Contains(snap.message, "需要重启") {
		t.Fatalf("final %+v", snap)
	}
	if got := readFile(t, exe); got != "payload-v1.10.0" {
		t.Fatalf("exe %q", got)
	}
	if got := readFile(t, exe+".backup"); got != "old-binary" {
		t.Fatalf("backup %q", got)
	}
	if !fake.saw("Bearer "+token) || !fake.saw("application/octet-stream") {
		t.Fatalf("auth/accept missing: %v", fake.requests())
	}
	if fake.saw("/assets/poison/") {
		t.Fatal("downloaded browser poison asset")
	}
}

func TestUpdateRollbackRules(t *testing.T) {
	srv, fake := newGitHub(t)
	fake.set(sampleReleases(srv.URL))
	exe := writeExe(t, "current-v1.9.0")
	m := newTestManager(t, srv, "v1.9.0", exe)

	high := call(t, m, http.MethodPost, "/api/v1/system/rollback", `{"version":"v1.10.0"}`)
	if high.Code != http.StatusBadRequest || !strings.Contains(high.Body.String(), "更高版本") {
		t.Fatalf("high %d %s", high.Code, high.Body)
	}
	unknown := call(t, m, http.MethodPost, "/api/v1/system/rollback", `{"version":"v1.0.1"}`)
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "未知版本") {
		t.Fatalf("unknown %d %s", unknown.Code, unknown.Body)
	}
	if m.snapshot().state != stateIdle || readFile(t, exe) != "current-v1.9.0" {
		t.Fatal("rejected rollback changed state or binary")
	}

	old := call(t, m, http.MethodPost, "/api/v1/system/update", `{"version":"v1.2.0"}`)
	if old.Code != http.StatusOK {
		t.Fatalf("older update %d %s", old.Code, old.Body)
	}
	if snap := waitTerminal(t, m); snap.state != stateDone || readFile(t, exe) != "payload-v1.2.0" {
		t.Fatalf("older install %+v %q", snap, readFile(t, exe))
	}

	m2 := newTestManager(t, srv, "v1.9.0", writeExe(t, "current-v1.9.0"))
	back := call(t, m2, http.MethodPost, "/api/v1/system/rollback", `{"version":"v1.2.0"}`)
	if back.Code != http.StatusOK {
		t.Fatalf("rollback %d %s", back.Code, back.Body)
	}
	if snap := waitTerminal(t, m2); snap.state != stateDone || readFile(t, m2.exePath) != "payload-v1.2.0" {
		t.Fatalf("historical rollback %+v %q", snap, readFile(t, m2.exePath))
	}
}

func TestEmptyRollbackRestoresBackupOnly(t *testing.T) {
	exe := writeExe(t, "new-binary")
	if err := os.WriteFile(exe+".backup", []byte("backed-up"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := linuxManager(t, "v1.2.0", exe)
	rr := call(t, m, http.MethodPost, "/api/v1/system/rollback", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("rollback %d %s", rr.Code, rr.Body)
	}
	snap := waitTerminal(t, m)
	if snap.state != stateDone || !snap.needRestart || readFile(t, exe) != "backed-up" {
		t.Fatalf("restored %+v %q", snap, readFile(t, exe))
	}
	missing := call(t, linuxManager(t, "v1.2.0", writeExe(t, "only")), http.MethodPost, "/api/v1/system/rollback", "{}")
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "没有可恢复的本地备份") {
		t.Fatalf("missing %d %s", missing.Code, missing.Body)
	}
}

func TestDevAndNonLinuxCannotInstall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)
	exe := writeExe(t, "dev-binary")
	dev := newTestManager(t, srv, "dev", exe)
	rr := call(t, dev, http.MethodPost, "/api/v1/system/update", "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "开发构建") {
		t.Fatalf("dev %d %s", rr.Code, rr.Body)
	}
	view := decodeStatus(t, call(t, dev, http.MethodGet, "/api/v1/releases", ""))
	if view.CanUpdate || view.CanRollback {
		t.Fatalf("dev flags %+v", view)
	}
	win := newTestManager(t, srv, "v1.2.3", exe)
	win.goos = "windows"
	bad := call(t, win, http.MethodPost, "/api/v1/system/rollback", "")
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "仅 Linux") {
		t.Fatalf("windows %d %s", bad.Code, bad.Body)
	}
	restart := call(t, win, http.MethodPost, "/api/v1/system/restart", "")
	if restart.Code != http.StatusBadRequest || !strings.Contains(restart.Body.String(), "手动重启") {
		t.Fatalf("restart %d %s", restart.Code, restart.Body)
	}
}

func TestRestartExitsOnlyOnLinuxAfterResponse(t *testing.T) {
	exitCh := make(chan int, 1)
	m := linuxManager(t, "v1.2.0", writeExe(t, "bin"))
	m.restartWait = 40 * time.Millisecond
	m.exit = func(code int) { exitCh <- code }
	rr := call(t, m, http.MethodPost, "/api/v1/system/restart", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"restarting":true`) {
		t.Fatalf("restart %d %s", rr.Code, rr.Body)
	}
	select {
	case code := <-exitCh:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("exit was not called")
	}
	win := linuxManager(t, "v1.2.0", writeExe(t, "bin"))
	win.goos = "windows"
	called := false
	win.exit = func(int) { called = true }
	if rr := call(t, win, http.MethodPost, "/api/v1/system/restart", ""); rr.Code != http.StatusBadRequest {
		t.Fatal(rr.Body)
	}
	time.Sleep(60 * time.Millisecond)
	if called {
		t.Fatal("non-linux restart exited")
	}
}

func TestStrictJSONAndArchiveFailures(t *testing.T) {
	srv, fake := newGitHub(t)
	fake.set(sampleReleases(srv.URL))
	exe := writeExe(t, "old-binary")
	m := newTestManager(t, srv, "v1.2.3", exe)
	for _, tc := range []struct {
		body string
		want string
	}{
		{`{"version":"v1.2.0","extra":1}`, "未知字段"},
		{`{"version":1}`, "请求 JSON 无效"},
		{`{"version":"v1.2.0"}{"version":"v1.2.0"}`, "多余内容"},
	} {
		rr := call(t, m, http.MethodPost, "/api/v1/system/update", tc.body)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
			t.Fatalf("body %s => %d %s", tc.body, rr.Code, rr.Body)
		}
	}
	huge := bytes.Repeat([]byte("a"), maxBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update", bytes.NewReader(huge))
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	m.Register(mux)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "1MB") {
		t.Fatalf("huge %d %s", rec.Code, rec.Body)
	}
	if m.snapshot().state != stateIdle {
		t.Fatal(m.snapshot())
	}

	badSum := sampleReleases(srv.URL)
	for i := range badSum {
		if badSum[i].version == "v1.10.0" {
			badSum[i].checksum = []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  nodepool_v1.10.0_linux_amd64.tar.gz\n")
		}
	}
	fake.set(badSum)
	if rr := call(t, m, http.MethodPost, "/api/v1/system/update", ""); rr.Code != http.StatusOK {
		t.Fatal(rr.Body)
	}
	snap := waitTerminal(t, m)
	if snap.state != stateError || !strings.Contains(snap.message, "SHA256") || readFile(t, exe) != "old-binary" || regularFile(exe+".backup") {
		t.Fatalf("checksum %+v backup=%v exe=%q", snap, regularFile(exe+".backup"), readFile(t, exe))
	}
}

func TestArchiveGuardsAndReplaceRestore(t *testing.T) {
	cases := []struct {
		name  string
		files map[string][]byte
		link  bool
		limit int64
		want  string
	}{
		{name: "traversal", files: map[string][]byte{"../nodepool": []byte("pwned")}, want: "不安全"},
		{name: "multiple", files: map[string][]byte{"nodepool": []byte("ok"), "extra": []byte("x")}, want: "意外文件"},
		{name: "symlink", link: true, want: "不是普通文件"},
		{name: "oversize", files: map[string][]byte{"nodepool": []byte("0123456789abcdef")}, limit: 4, want: "过大"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var archive []byte
			if tc.link {
				archive = symlinkArchive(t)
			} else {
				archive = tarGz(t, tc.files)
			}
			srv, fake := newGitHub(t)
			rels := []fakeRel{{
				version: "v1.2.4", name: "Bad", body: "bad", when: time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC),
				archive: archive, checksum: checksumFile("nodepool_v1.2.4_linux_amd64.tar.gz", archive),
			}}
			fake.set(rels)
			exe := writeExe(t, "old-binary")
			m := newTestManager(t, srv, "v1.0.0", exe)
			if tc.limit > 0 {
				m.maxBinary = tc.limit
			}
			if rr := call(t, m, http.MethodPost, "/api/v1/system/update", ""); rr.Code != http.StatusOK {
				t.Fatal(rr.Body)
			}
			snap := waitTerminal(t, m)
			if snap.state != stateError || !strings.Contains(snap.message, tc.want) || readFile(t, exe) != "old-binary" {
				t.Fatalf("%+v exe=%q", snap, readFile(t, exe))
			}
		})
	}

	srv, fake := newGitHub(t)
	fake.set(sampleReleases(srv.URL))
	exe := writeExe(t, "old-binary")
	m := newTestManager(t, srv, "v1.2.3", exe)
	orig := m.rename
	var calls int
	m.rename = func(a, b string) error {
		calls++
		if calls == 2 {
			return errors.New("injected rename failure")
		}
		return orig(a, b)
	}
	if rr := call(t, m, http.MethodPost, "/api/v1/system/update", ""); rr.Code != http.StatusOK {
		t.Fatal(rr.Body)
	}
	snap := waitTerminal(t, m)
	if snap.state != stateError || !strings.Contains(snap.message, "injected rename failure") || !strings.Contains(snap.message, "已恢复") || readFile(t, exe) != "old-binary" {
		t.Fatalf("restore %+v %q", snap, readFile(t, exe))
	}
}

func TestUntrustedAssetAndRedirect(t *testing.T) {
	srv, fake := newGitHub(t)
	rels := sampleReleases(srv.URL)
	for i := range rels {
		if rels[i].version == "v1.10.0" {
			rels[i].assetURL = "https://evil.example/nodepool.tar.gz"
		}
	}
	fake.set(rels)
	exe := writeExe(t, "old-binary")
	m := newTestManager(t, srv, "v1.2.3", exe)
	rr := call(t, m, http.MethodPost, "/api/v1/system/update", "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "不可信") || m.snapshot().state != stateIdle {
		t.Fatalf("evil %d %s", rr.Code, rr.Body)
	}
	if fake.saw("evil.example") {
		t.Fatal("contacted untrusted host")
	}

	token := "upd-token-9f3a"
	t.Setenv("UPDATE_GITHUB_TOKEN", token)
	var mu sync.Mutex
	var seen []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		seen = append(seen, r.URL.Host+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.URL.Host {
		case "api.github.com":
			return &http.Response{
				StatusCode: http.StatusFound, Status: "302 Found",
				Header: http.Header{"Location": []string{"https://objects.githubusercontent.com/asset"}},
				Body:   io.NopCloser(strings.NewReader("")), ContentLength: 0, Request: r,
			}, nil
		case "objects.githubusercontent.com":
			return &http.Response{
				StatusCode: 200, Status: "200 OK", Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader("payload")), ContentLength: 7, Request: r,
			}, nil
		default:
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
	})
	dir := t.TempDir()
	dm, err := newManager(managerConfig{
		repository: "LectWolf/mihomo-nodepool", version: "v1.0.0", apiBase: githubAPI,
		httpClient: &http.Client{Transport: transport}, exePath: filepath.Join(dir, "nodepool"),
		goos: "linux", goarch: "amd64", exit: func(int) { t.Error("exit") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dm.exePath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	if err := dm.downloadToFile(context.Background(), githubAPI+"/repos/LectWolf/mihomo-nodepool/releases/assets/1", dest, 100, dm.token()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || !strings.Contains(seen[0], "Bearer "+token) || strings.Contains(seen[1], token) {
		t.Fatalf("redirect auth = %v", seen)
	}
	evil := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" {
			t.Errorf("followed untrusted redirect to %s", r.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusFound, Status: "302 Found",
			Header: http.Header{"Location": []string{"https://evil.example/x"}},
			Body:   io.NopCloser(strings.NewReader("")), ContentLength: 0, Request: r,
		}, nil
	})
	dm.client = dm.withRedirects(&http.Client{Transport: evil})
	err = dm.downloadToFile(context.Background(), githubAPI+"/repos/LectWolf/mihomo-nodepool/releases/assets/2", filepath.Join(dir, "nope"), 100, token)
	if err == nil || !strings.Contains(err.Error(), "未信任") || strings.Contains(err.Error(), token) {
		t.Fatalf("untrusted redirect: %v", err)
	}
}

func TestPackageDoesNotTouchDatabase(t *testing.T) {
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("database/sql")) || bytes.Contains(body, []byte("go-sql-driver")) {
			t.Fatalf("%s references a database", name)
		}
	}
}

type fakeRel struct {
	version  string
	name     string
	body     string
	when     time.Time
	draft    bool
	pre      bool
	tag      string
	archive  []byte
	checksum []byte
	assetURL string
}

type gitFake struct {
	srv      *httptest.Server
	mu       sync.Mutex
	rels     []fakeRel
	listHits int
	fail     string
	block    <-chan struct{}
	seen     []string
}

func newGitHub(t *testing.T) (*httptest.Server, *gitFake) {
	t.Helper()
	fake := &gitFake{}
	fake.srv = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.srv.Close)
	return fake.srv, fake
}

func (g *gitFake) set(rels []fakeRel) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rels = rels
	g.fail = ""
}

func (g *gitFake) setFail(msg string) {
	g.mu.Lock()
	g.fail = msg
	g.mu.Unlock()
}

func (g *gitFake) hits() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.listHits
}

func (g *gitFake) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.seen...)
}

func (g *gitFake) saw(part string) bool {
	for _, line := range g.requests() {
		if strings.Contains(line, part) {
			return true
		}
	}
	return false
}

func (g *gitFake) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.seen = append(g.seen, r.URL.RequestURI()+" auth="+r.Header.Get("Authorization")+" accept="+r.Header.Get("Accept"))
	fail := g.fail
	rels := append([]fakeRel(nil), g.rels...)
	block := g.block
	if strings.HasSuffix(r.URL.Path, "/releases") {
		g.listHits++
	}
	g.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/releases") {
		if fail != "" {
			http.Error(w, fail, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(g.payload(rels))
		return
	}
	if strings.Contains(r.URL.Path, "/assets/archive/") && block != nil {
		<-block
	}
	for _, rel := range rels {
		switch r.URL.Path {
		case "/assets/archive/" + rel.version:
			_, _ = w.Write(rel.archive)
			return
		case "/assets/checksum/" + rel.version:
			_, _ = w.Write(rel.checksum)
			return
		case "/assets/poison/" + rel.version:
			_, _ = w.Write([]byte("POISON"))
			return
		}
	}
	http.NotFound(w, r)
}

func (g *gitFake) payload(rels []fakeRel) []ghRelease {
	out := make([]ghRelease, 0, len(rels))
	for _, rel := range rels {
		tag := rel.tag
		if tag == "" {
			tag = rel.version
		}
		item := ghRelease{
			TagName: tag, Name: rel.name, Body: rel.body, Draft: rel.draft, Prerelease: rel.pre,
			PublishedAt: rel.when, HTMLURL: "https://github.com/LectWolf/mihomo-nodepool/releases/tag/" + rel.version,
		}
		if rel.archive != nil {
			assetURL := rel.assetURL
			if assetURL == "" {
				assetURL = g.srv.URL + "/assets/archive/" + rel.version
			}
			item.Assets = append(item.Assets, ghAsset{
				Name: "nodepool_" + rel.version + "_linux_amd64.tar.gz", URL: assetURL,
				BrowserDownloadURL: g.srv.URL + "/assets/poison/" + rel.version, Size: int64(len(rel.archive)),
			})
		}
		if rel.checksum != nil {
			item.Assets = append(item.Assets, ghAsset{
				Name: "checksums.txt", URL: g.srv.URL + "/assets/checksum/" + rel.version, Size: int64(len(rel.checksum)),
			})
		}
		out = append(out, item)
	}
	return out
}

func sampleReleases(base string) []fakeRel {
	_ = base
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	versions := []struct {
		ver, name, body string
		draft, pre      bool
		tag             string
	}{
		{"v1.2.0", "Two", "two notes", false, false, ""},
		{"v1.10.0", "Ten", "ten notes", false, false, ""},
		{"v1.9.0", "Nine", "nine notes", false, false, ""},
		{"v1.11.0", "RC", "skip", false, true, "v1.11.0"},
		{"v2.0.0", "Draft", "skip", true, false, ""},
		{"v1.8.0", "Weird", "skip", false, false, "release-1.8.0"},
	}
	out := make([]fakeRel, 0, len(versions))
	for _, v := range versions {
		payload := "payload-" + v.ver
		archive := tarGz(nil, map[string][]byte{"nodepool": []byte(payload)})
		name := "nodepool_" + v.ver + "_linux_amd64.tar.gz"
		out = append(out, fakeRel{
			version: v.ver, name: v.name, body: v.body, when: when, draft: v.draft, pre: v.pre, tag: v.tag,
			archive: archive, checksum: checksumFile(name, archive),
		})
	}
	return out
}

func tarGz(t *testing.T, files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			if t != nil {
				t.Fatal(err)
			}
			panic(err)
		}
		if _, err := tw.Write(body); err != nil {
			if t != nil {
				t.Fatal(err)
			}
			panic(err)
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func symlinkArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	hdr := &tar.Header{Name: "nodepool", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/tmp/evil"}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil || zw.Close() != nil {
		t.Fatal("close symlink archive")
	}
	return buf.Bytes()
}

func checksumFile(name string, data []byte) []byte {
	sum := sha256.Sum256(data)
	return []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
}

func newTestManager(t *testing.T, srv *httptest.Server, version, exe string) *Manager {
	t.Helper()
	m, err := newManager(managerConfig{
		repository: "LectWolf/mihomo-nodepool", version: version, apiBase: srv.URL,
		httpClient: srv.Client(), exePath: exe, goos: "linux", goarch: "amd64",
		exit: func(int) { t.Errorf("unexpected exit") }, cacheTTL: 45 * time.Second,
		restartWait: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func linuxManager(t *testing.T, version, exe string) *Manager {
	t.Helper()
	m, err := newManager(managerConfig{
		repository: "LectWolf/mihomo-nodepool", version: version, apiBase: "http://127.0.0.1:9",
		exePath: exe, goos: "linux", goarch: "amd64", exit: func(int) { t.Errorf("unexpected exit") },
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func writeExe(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nodepool")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func call(t *testing.T, m *Manager, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	m.Register(mux)
	var reader io.Reader
	if body != "" || method == http.MethodPost {
		reader = strings.NewReader(body)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(method, path, reader))
	return rr
}

func decodeStatus(t *testing.T, rr *httptest.ResponseRecorder) statusView {
	t.Helper()
	var view statusView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func waitState(t *testing.T, m *Manager, want string) snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap := m.snapshot()
		if snap.state == want {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s, got %+v", want, m.snapshot())
	return snapshot{}
}

func waitTerminal(t *testing.T, m *Manager) snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap := m.snapshot()
		if !snap.busy && (snap.state == stateDone || snap.state == stateError) {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for terminal state, got %+v", m.snapshot())
	return snapshot{}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
