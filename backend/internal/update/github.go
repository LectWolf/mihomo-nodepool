package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	maxListBytes     = 2 << 20
	maxChecksumBytes = 1 << 20
)

type ghAsset struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []ghAsset `json:"assets"`
}

type releaseRecord struct {
	Version     string
	Name        string
	Body        string
	PublishedAt time.Time
	URL         string
	Assets      []ghAsset
}

func (m *Manager) token() string {
	return strings.TrimSpace(os.Getenv("UPDATE_GITHUB_TOKEN"))
}

func (m *Manager) safe(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if token := m.token(); token != "" {
		msg = strings.ReplaceAll(msg, token, "[redacted]")
	}
	return msg
}

func (m *Manager) withRedirects(base *http.Client) *http.Client {
	client := &http.Client{Timeout: 2 * time.Minute}
	if base != nil {
		client.Transport = base.Transport
		client.Jar = base.Jar
		client.Timeout = base.Timeout
		client.CheckRedirect = base.CheckRedirect
	}
	prev := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("重定向次数过多")
		}
		if err := m.redirectPolicy(req, via); err != nil {
			return err
		}
		if prev != nil {
			return prev(req, via)
		}
		return nil
	}
	return client
}

func (m *Manager) redirectPolicy(req *http.Request, via []*http.Request) error {
	if !m.trusted(req.URL) {
		return fmt.Errorf("拒绝跳转到未信任的地址")
	}
	if len(via) > 0 && !sameOrigin(via[0].URL, req.URL) {
		req.Header.Del("Authorization")
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (m *Manager) trusted(u *url.URL) bool {
	if u == nil || u.User != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Scheme, m.apiScheme) && strings.EqualFold(u.Host, m.apiHost) {
		return true
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	if port := u.Port(); port != "" && port != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "api.github.com", "github.com", "objects.githubusercontent.com",
		"release-assets.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	}
	return strings.HasSuffix(host, ".githubusercontent.com") && !strings.Contains(host, "..")
}

func (m *Manager) isAPIHost(u *url.URL) bool {
	if u == nil || u.User != nil {
		return false
	}
	if strings.EqualFold(u.Scheme, m.apiScheme) && strings.EqualFold(u.Host, m.apiHost) {
		return true
	}
	return strings.EqualFold(u.Scheme, "https") && strings.EqualFold(u.Hostname(), "api.github.com") && (u.Port() == "" || u.Port() == "443")
}

func (m *Manager) validateDownloadURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || !m.trusted(parsed) {
		return fmt.Errorf("发布资源地址不可信")
	}
	return nil
}

func (m *Manager) listReleases(ctx context.Context) ([]releaseRecord, error) {
	m.mu.Lock()
	if m.cached && m.cacheTTL > 0 && m.now().Sub(m.cachedAt) < m.cacheTTL {
		items := cloneReleases(m.cache)
		m.mu.Unlock()
		return items, nil
	}
	m.mu.Unlock()

	items, err := m.fetchReleases(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if m.cached {
			return cloneReleases(m.cache), err
		}
		return nil, err
	}
	m.cache = items
	m.cached = true
	m.cachedAt = m.now()
	return cloneReleases(items), nil
}

func cloneReleases(in []releaseRecord) []releaseRecord {
	out := make([]releaseRecord, len(in))
	copy(out, in)
	for i := range out {
		out[i].Assets = append([]ghAsset(nil), in[i].Assets...)
	}
	return out
}

func (m *Manager) fetchReleases(ctx context.Context) ([]releaseRecord, error) {
	endpoint, err := m.releasesURL()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, err := m.fetchBytes(ctx, endpoint, "application/vnd.github+json", m.token(), maxListBytes)
	if err != nil {
		return nil, fmt.Errorf("获取发布列表失败: %w", err)
	}
	var releases []ghRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("获取发布列表失败: %w", err)
	}
	return stableReleases(releases), nil
}

func (m *Manager) releasesURL() (string, error) {
	base, err := url.Parse(m.apiBase)
	if err != nil {
		return "", err
	}
	owner, name, _ := strings.Cut(m.repo, "/")
	u := base.JoinPath("repos", owner, name, "releases")
	q := u.Query()
	q.Set("per_page", "30")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func stableReleases(in []ghRelease) []releaseRecord {
	out := make([]releaseRecord, 0, len(in))
	seen := make(map[string]int, len(in))
	for _, rel := range in {
		if rel.Draft || rel.Prerelease {
			continue
		}
		_, ok := parseStable(rel.TagName)
		if !ok {
			continue
		}
		item := releaseRecord{
			Version: rel.TagName, Name: rel.Name, Body: rel.Body,
			PublishedAt: rel.PublishedAt, URL: rel.HTMLURL,
			Assets: append([]ghAsset(nil), rel.Assets...),
		}
		if item.Name == "" {
			item.Name = item.Version
		}
		if idx, dup := seen[item.Version]; dup {
			if item.PublishedAt.After(out[idx].PublishedAt) {
				out[idx] = item
			}
			continue
		}
		seen[item.Version] = len(out)
		out = append(out, item)
	}
	sortReleases(out)
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

func sortReleases(items []releaseRecord) {
	for i := 1; i < len(items); i++ {
		item := items[i]
		j := i
		for j > 0 && releaseLess(item, items[j-1]) {
			items[j] = items[j-1]
			j--
		}
		items[j] = item
	}
}

func releaseLess(a, b releaseRecord) bool {
	av, _ := parseStable(a.Version)
	bv, _ := parseStable(b.Version)
	c := compareSemver(av, bv)
	if c != 0 {
		return c > 0
	}
	return a.PublishedAt.After(b.PublishedAt)
}

func (m *Manager) fetchBytes(ctx context.Context, rawURL, accept, token string, maxSize int64) ([]byte, error) {
	if err := m.validateDownloadURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token != "" && m.isAPIHost(req.URL) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpStatusError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("文件超过大小限制 %d", maxSize)
	}
	return data, nil
}

func (m *Manager) downloadToFile(ctx context.Context, rawURL, dest string, maxSize int64, token string) error {
	if err := m.validateDownloadURL(rawURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/octet-stream")
	if token != "" && m.isAPIHost(req.URL) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpStatusError(resp)
	}
	if resp.ContentLength > maxSize {
		return fmt.Errorf("文件过大: %d", resp.ContentLength)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxSize+1))
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || n > maxSize {
		_ = os.Remove(dest)
		if n > maxSize {
			return fmt.Errorf("文件超过大小限制 %d", maxSize)
		}
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	}
	return nil
}

func httpStatusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	text := strings.TrimSpace(string(body))
	if text == "" {
		return fmt.Errorf("GitHub API 返回 %d", resp.StatusCode)
	}
	return fmt.Errorf("GitHub API 返回 %d: %s", resp.StatusCode, text)
}
