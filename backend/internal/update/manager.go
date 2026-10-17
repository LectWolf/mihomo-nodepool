package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo"
)

const (
	defaultRepository = "LectWolf/mihomo-nodepool"
	githubAPI         = "https://api.github.com"
	userAgent         = "mihomo-nodepool"
	maxBodyBytes      = 1 << 20
	defaultCacheTTL   = 45 * time.Second
	restartDelay      = 500 * time.Millisecond

	stateIdle        = "idle"
	stateDownloading = "downloading"
	stateInstalling  = "installing"
	stateDone        = "done"
	stateError       = "error"
	stateRestarting  = "restarting"
)

// Options configures the production updater. Paths, HTTP clients, API bases,
// operating system, and process exit are injected only through the unexported
// constructor used by tests.
type Options struct {
	Repository string
	Version    string
}

// Manager serves the version API and runs one update or rollback at a time.
type Manager struct {
	repo        string
	version     string
	apiBase     string
	apiHost     string
	apiScheme   string
	client      *http.Client
	exePath     string
	goos        string
	goarch      string
	exit        func(int)
	cacheTTL    time.Duration
	restartWait time.Duration
	opTimeout   time.Duration
	maxArchive  int64
	maxBinary   int64
	rename      func(string, string) error
	now         func() time.Time

	mu          sync.Mutex
	busy        bool
	state       string
	message     string
	needRestart bool
	cached      bool
	cache       []releaseRecord
	cachedAt    time.Time
}

type managerConfig struct {
	repository  string
	version     string
	apiBase     string
	httpClient  *http.Client
	exePath     string
	goos        string
	goarch      string
	exit        func(int)
	cacheTTL    time.Duration
	restartWait time.Duration
	opTimeout   time.Duration
	maxArchive  int64
	maxBinary   int64
	rename      func(string, string) error
	now         func() time.Time
}

// New builds a production updater. Repository defaults to LectWolf/mihomo-nodepool.
// Version defaults to the compiled build version.
func New(opts Options) (*Manager, error) {
	repo := strings.TrimSpace(opts.Repository)
	if repo == "" {
		repo = defaultRepository
	}
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = buildinfo.Version
	}
	return newManager(managerConfig{
		repository:  repo,
		version:     version,
		apiBase:     githubAPI,
		goos:        runtime.GOOS,
		goarch:      runtime.GOARCH,
		exit:        os.Exit,
		cacheTTL:    defaultCacheTTL,
		restartWait: restartDelay,
	})
}

func newManager(cfg managerConfig) (*Manager, error) {
	repo, err := normalizeRepo(cfg.repository)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.apiBase), "/")
	if base == "" {
		base = githubAPI
	}
	parsed, err := parseBase(base)
	if err != nil {
		return nil, err
	}
	goos := cfg.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := cfg.goarch
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	cacheTTL := cfg.cacheTTL
	switch {
	case cacheTTL < 0:
		cacheTTL = 0
	case cacheTTL == 0:
		cacheTTL = defaultCacheTTL
	}
	wait := cfg.restartWait
	if wait <= 0 {
		wait = restartDelay
	}
	opTimeout := cfg.opTimeout
	if opTimeout <= 0 {
		opTimeout = 3 * time.Minute
	}
	maxArchive := cfg.maxArchive
	if maxArchive <= 0 {
		maxArchive = 128 << 20
	}
	maxBinary := cfg.maxBinary
	if maxBinary <= 0 {
		maxBinary = 128 << 20
	}
	rename := cfg.rename
	if rename == nil {
		rename = os.Rename
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	exit := cfg.exit
	if exit == nil {
		exit = func(int) {}
	}
	exePath, err := resolveExecutable(cfg.exePath)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		repo:        repo,
		version:     strings.TrimSpace(cfg.version),
		apiBase:     parsed.String(),
		apiHost:     parsed.Host,
		apiScheme:   parsed.Scheme,
		exePath:     exePath,
		goos:        goos,
		goarch:      goarch,
		exit:        exit,
		cacheTTL:    cacheTTL,
		restartWait: wait,
		opTimeout:   opTimeout,
		maxArchive:  maxArchive,
		maxBinary:   maxBinary,
		rename:      rename,
		now:         now,
		state:       stateIdle,
	}
	m.client = m.withRedirects(cfg.httpClient)
	return m, nil
}

// Register mounts the version routes. The parent server owns authentication.
func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/releases", m.handleReleases)
	mux.HandleFunc("POST /api/v1/system/update", m.handleUpdate)
	mux.HandleFunc("POST /api/v1/system/rollback", m.handleRollback)
	mux.HandleFunc("POST /api/v1/system/restart", m.handleRestart)
}

type statusView struct {
	CurrentVersion  string        `json:"current_version"`
	LatestVersion   string        `json:"latest_version"`
	UpdateAvailable bool          `json:"update_available"`
	CanUpdate       bool          `json:"can_update"`
	CanRollback     bool          `json:"can_rollback"`
	NeedRestart     bool          `json:"need_restart"`
	State           string        `json:"state"`
	Message         string        `json:"message"`
	Releases        []releaseView `json:"releases"`
}

type releaseView struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url"`
}

func (m *Manager) handleReleases(w http.ResponseWriter, r *http.Request) {
	items, listErr := m.listReleases(r.Context())
	writeJSON(w, http.StatusOK, m.statusFrom(m.snapshot(), items, listErr))
}

func (m *Manager) handleUpdate(w http.ResponseWriter, r *http.Request) {
	version, _, ok := readVersion(w, r)
	if !ok {
		return
	}
	if code, msg := m.guardInstall(); code != 0 {
		writeError(w, code, msg)
		return
	}
	if version != "" {
		if _, stable := parseStable(version); !stable {
			writeError(w, http.StatusBadRequest, "版本号必须是已发布的 vX.Y.Z")
			return
		}
	}
	items, err := m.listReleases(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, m.safe(err))
		return
	}
	target, okTarget := selectTarget(items, version, false)
	if !okTarget {
		if version == "" {
			writeError(w, http.StatusBadRequest, "没有可用的稳定版本")
		} else {
			writeError(w, http.StatusBadRequest, "版本不在已发布的稳定版本列表中")
		}
		return
	}
	if _, _, _, err := m.resolveAssets(target); err != nil {
		writeError(w, http.StatusBadRequest, m.safe(err))
		return
	}
	if !m.tryBegin(stateDownloading, "正在下载 "+target.Version) {
		writeError(w, http.StatusConflict, "已有更新或回退正在执行")
		return
	}
	go m.runInstall(target)
	writeJSON(w, http.StatusOK, map[string]bool{"queued": true})
}

func (m *Manager) handleRollback(w http.ResponseWriter, r *http.Request) {
	version, _, ok := readVersion(w, r)
	if !ok {
		return
	}
	if code, msg := m.guardInstall(); code != 0 {
		writeError(w, code, msg)
		return
	}
	if version == "" {
		exe := m.exePath
		if !regularFile(exe + ".backup") {
			writeError(w, http.StatusBadRequest, "没有可恢复的本地备份")
			return
		}
		if !m.tryBegin(stateInstalling, "正在恢复本地备份") {
			writeError(w, http.StatusConflict, "已有更新或回退正在执行")
			return
		}
		go m.runLocalRollback()
		writeJSON(w, http.StatusOK, map[string]bool{"queued": true})
		return
	}
	if _, stable := parseStable(version); !stable {
		writeError(w, http.StatusBadRequest, "不能回退到未知版本")
		return
	}
	if cmp, okCmp := compareVersions(version, m.version); !okCmp || cmp >= 0 {
		writeError(w, http.StatusBadRequest, "不能回退到当前或更高版本")
		return
	}
	items, err := m.listReleases(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, m.safe(err))
		return
	}
	target, okTarget := selectTarget(items, version, true)
	if !okTarget {
		writeError(w, http.StatusBadRequest, "不能回退到未知版本")
		return
	}
	if _, _, _, err := m.resolveAssets(target); err != nil {
		writeError(w, http.StatusBadRequest, m.safe(err))
		return
	}
	if !m.tryBegin(stateDownloading, "正在下载 "+target.Version) {
		writeError(w, http.StatusConflict, "已有更新或回退正在执行")
		return
	}
	go m.runInstall(target)
	writeJSON(w, http.StatusOK, map[string]bool{"queued": true})
}

func (m *Manager) handleRestart(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	_, _ = io.Copy(io.Discard, r.Body)
	if m.goos != "linux" {
		writeError(w, http.StatusBadRequest, "仅 Linux 会退出进程并由 Docker 重启策略拉起，当前系统请手动重启")
		return
	}
	if !m.tryBegin(stateRestarting, "正在重启") {
		writeError(w, http.StatusConflict, "已有更新、回退或重启正在执行")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"restarting": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	delay := m.restartWait
	exit := m.exit
	go func() {
		time.Sleep(delay)
		exit(0)
	}()
}

func (m *Manager) guardInstall() (int, string) {
	if m.goos != "linux" {
		return http.StatusBadRequest, "仅 Linux 可以更换程序文件"
	}
	if _, ok := parseStable(m.version); !ok {
		return http.StatusBadRequest, "开发构建不能安装或回退"
	}
	if m.isBusy() {
		return http.StatusConflict, "已有更新或回退正在执行"
	}
	return 0, ""
}

type snapshot struct {
	state       string
	message     string
	needRestart bool
	busy        bool
}

func (m *Manager) snapshot() snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return snapshot{state: m.state, message: m.message, needRestart: m.needRestart, busy: m.busy}
}

func (m *Manager) isBusy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.busy
}

func (m *Manager) tryBegin(state, message string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy {
		return false
	}
	m.busy = true
	m.state = state
	m.message = message
	return true
}

func (m *Manager) setProgress(state, message string) {
	m.mu.Lock()
	m.state = state
	m.message = message
	m.mu.Unlock()
}

func (m *Manager) finish(state, message string, restart bool) {
	m.mu.Lock()
	m.busy = false
	m.state = state
	m.message = message
	if restart {
		m.needRestart = true
	}
	m.mu.Unlock()
}

func (m *Manager) statusFrom(snap snapshot, items []releaseRecord, listErr error) statusView {
	views := make([]releaseView, 0, len(items))
	latest := ""
	hasOlder := false
	currentStable, currentOK := parseStable(m.version)
	for i, item := range items {
		if i == 0 {
			latest = item.Version
		}
		if currentOK {
			if rel, ok := parseStable(item.Version); ok && compareSemver(rel, currentStable) < 0 {
				hasOlder = true
			}
		}
		published := ""
		if !item.PublishedAt.IsZero() {
			published = item.PublishedAt.UTC().Format(time.RFC3339)
		}
		views = append(views, releaseView{
			Version: item.Version, Name: item.Name, Body: item.Body,
			PublishedAt: published, URL: item.URL,
		})
	}
	eligible := m.goos == "linux" && currentOK && !snap.busy
	updateAvailable := false
	if latest != "" {
		if !currentOK {
			updateAvailable = true
		} else if rel, ok := parseStable(latest); ok && compareSemver(rel, currentStable) > 0 {
			updateAvailable = true
		}
	}
	message := snap.message
	if listErr != nil {
		real := m.safe(listErr)
		switch {
		case message == "" || snap.state == stateIdle:
			message = real
		case !strings.Contains(message, real):
			message = message + "；" + real
		}
	}
	return statusView{
		CurrentVersion:  m.version,
		LatestVersion:   latest,
		UpdateAvailable: updateAvailable,
		CanUpdate:       eligible && len(items) > 0,
		CanRollback:     eligible && (m.backupExists() || hasOlder),
		NeedRestart:     snap.needRestart,
		State:           snap.state,
		Message:         message,
		Releases:        views,
	}
}

func (m *Manager) runInstall(target releaseRecord) {
	defer func() {
		if rec := recover(); rec != nil {
			m.finish(stateError, m.safe(fmt.Errorf("内部错误: %v", rec)), false)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), m.opTimeout)
	defer cancel()
	if err := m.applyRelease(ctx, target); err != nil {
		m.finish(stateError, m.safe(err), false)
		return
	}
	m.finish(stateDone, "安装完成，需要重启", true)
}

func (m *Manager) runLocalRollback() {
	defer func() {
		if rec := recover(); rec != nil {
			m.finish(stateError, m.safe(fmt.Errorf("内部错误: %v", rec)), false)
		}
	}()
	exe := m.exePath
	if err := m.restoreBackup(exe); err != nil {
		m.finish(stateError, m.safe(err), false)
		return
	}
	m.finish(stateDone, "已恢复本地备份，需要重启", true)
}

func readVersion(w http.ResponseWriter, r *http.Request) (string, bool, bool) {
	if r.ContentLength > maxBodyBytes {
		writeError(w, http.StatusBadRequest, "请求体不能超过 1MB")
		return "", false, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return "", false, true
		}
		writeDecodeError(w, err)
		return "", false, false
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体包含多余内容")
		return "", false, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", false, true
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	var body struct {
		Version *string `json:"version"`
	}
	if err := strict.Decode(&body); err != nil {
		writeDecodeError(w, err)
		return "", false, false
	}
	if err := strict.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体包含多余内容")
		return "", false, false
	}
	if body.Version == nil {
		return "", false, true
	}
	return strings.TrimSpace(*body.Version), true, true
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "request body too large") {
		writeError(w, http.StatusBadRequest, "请求体不能超过 1MB")
		return
	}
	if strings.Contains(err.Error(), "unknown field") {
		writeError(w, http.StatusBadRequest, "请求包含未知字段")
		return
	}
	writeError(w, http.StatusBadRequest, "请求 JSON 无效")
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func selectTarget(items []releaseRecord, version string, exact bool) (releaseRecord, bool) {
	if version == "" {
		if exact || len(items) == 0 {
			return releaseRecord{}, false
		}
		return items[0], true
	}
	for _, item := range items {
		if item.Version == version {
			return item, true
		}
	}
	return releaseRecord{}, false
}

func parseBase(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return nil, errors.New("更新 API 地址无效")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}
