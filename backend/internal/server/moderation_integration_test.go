package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/auth"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/core"
	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
)

func TestMySQLSourceTakedownAndUserDetail(t *testing.T) {
	s := apiStore(t)
	a, e := auth.New(t.TempDir(), "test-password")
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	mails := map[string]mailContent{}
	oldSend := sendMail
	sendMail = func(_ context.Context, _ pool.SMTPSettings, to string, m mailContent) error {
		mu.Lock()
		mails[to] = m
		mu.Unlock()
		return nil
	}
	defer func() { sendMail = oldSend }()
	h := New(Options{Service: pool.NewService(s, core.New()), Auth: a, Updates: testUpdates{}})
	call := func(method, path, body string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: status %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	login := func(body string) *http.Cookie {
		t.Helper()
		return call("POST", "/api/v1/auth/login", body, nil, 200).Result().Cookies()[0]
	}
	admin := login(`{"password":"test-password"}`)
	settings, e := s.Settings(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	settings.Login.SMTP = pool.SMTPSettings{Host: "smtp.test", Port: 465, Security: "ssl", From: "noreply@qq.com"}
	if e = s.SaveSettings(t.Context(), settings); e != nil {
		t.Fatal(e)
	}
	call("POST", "/api/v1/users", `{"username":"mgr","password":"manager-pass","role":"manager"}`, admin, 201)
	call("POST", "/api/v1/users", `{"username":"owner","password":"owner-pass1"}`, admin, 201)
	call("POST", "/api/v1/users", `{"username":"other","password":"other-pass1"}`, admin, 201)
	var ownerID, otherID, mgrID int64
	_ = s.DB.QueryRow(`SELECT id FROM np_users WHERE username='owner'`).Scan(&ownerID)
	_ = s.DB.QueryRow(`SELECT id FROM np_users WHERE username='other'`).Scan(&otherID)
	_ = s.DB.QueryRow(`SELECT id FROM np_users WHERE username='mgr'`).Scan(&mgrID)
	id := func(v int64) string { return strconv.FormatInt(v, 10) }

	// 管理员代为设置邮箱：视为已验证、可清空、重复报 409
	if body := call("PATCH", "/api/v1/users/"+id(ownerID), `{"email":" Owner@Example.org "}`, admin, 200).Body.String(); !strings.Contains(body, `"email":"owner@example.org"`) || !strings.Contains(body, `"email_verified":true`) {
		t.Fatalf("admin set email: %s", body)
	}
	call("PATCH", "/api/v1/users/"+id(otherID), `{"email":"owner@example.org"}`, admin, 409)
	call("PATCH", "/api/v1/users/"+id(otherID), `{"email":"not-an-email"}`, admin, 400)
	call("PATCH", "/api/v1/users/"+id(otherID), `{"email":"other@qq.com"}`, admin, 200)
	if body := call("PATCH", "/api/v1/users/"+id(otherID), `{"email":""}`, admin, 200).Body.String(); !strings.Contains(body, `"email":""`) || !strings.Contains(body, `"email_verified":false`) {
		t.Fatalf("clear email: %s", body)
	}
	call("PATCH", "/api/v1/users/"+id(otherID), `{"email":"other@qq.com"}`, admin, 200)
	call("PATCH", "/api/v1/users/"+id(mgrID), `{"email":"mgr@qq.com"}`, admin, 200)
	if body := call("GET", "/api/v1/users?q=owner@example", "", admin, 200).Body.String(); !strings.Contains(body, `"username":"owner"`) {
		t.Fatalf("search by email: %s", body)
	}

	owner := login(`{"username":"owner","password":"owner-pass1"}`)
	other := login(`{"username":"other","password":"other-pass1"}`)
	mgr := login(`{"username":"mgr","password":"manager-pass"}`)
	var src pool.Source
	_ = json.Unmarshal(call("POST", "/api/v1/sources", `{"name":"Owner airport","kind":"url","url":"https://example.com/sub?token=secret"}`, owner, 202).Body.Bytes(), &src)
	var adminSrc pool.Source
	_ = json.Unmarshal(call("POST", "/api/v1/sources", `{"name":"Admin airport","kind":"url","url":"https://example.com/admin?token=secret"}`, admin, 202).Body.Bytes(), &adminSrc)
	sid := id(src.ID)

	// 权限：普通用户不能下架，用户管理员不能处理管理员的来源
	call("POST", "/api/v1/sources/"+sid+"/takedown", `{"reason":"owner_request"}`, other, 403)
	call("POST", "/api/v1/sources/"+id(adminSrc.ID)+"/takedown", `{"reason":"owner_request"}`, mgr, 403)
	call("POST", "/api/v1/sources/"+sid+"/takedown", `{"reason":"nope"}`, mgr, 400)
	call("POST", "/api/v1/sources/"+sid+"/takedown", `{"reason":"other"}`, mgr, 400)
	call("GET", "/api/v1/moderation/sources", "", other, 403)
	call("POST", "/api/v1/sources/"+sid+"/restore", `{}`, mgr, 400)

	body := call("POST", "/api/v1/sources/"+sid+"/takedown", `{"reason":"owner_request","detail":"机场主邮件要求停止公开分享"}`, mgr, 200).Body.String()
	if !strings.Contains(body, `"enabled":false`) || !strings.Contains(body, `"reason_label":"订阅链接所有方要求下架"`) || !strings.Contains(body, `"notified":true`) {
		t.Fatalf("takedown response: %s", body)
	}
	// 提供方看到下架原因，但不能自行启用
	if body = call("GET", "/api/v1/sources", "", owner, 200).Body.String(); !strings.Contains(body, `"detail":"机场主邮件要求停止公开分享"`) || !strings.Contains(body, `"by":"mgr"`) {
		t.Fatalf("owner should see takedown: %s", body)
	}
	call("PATCH", "/api/v1/sources/"+sid, `{"enabled":true}`, owner, 400)
	call("PATCH", "/api/v1/sources/"+sid, `{"enabled":true}`, admin, 400)
	if body = call("GET", "/api/v1/sources/catalog", "", other, 200).Body.String(); strings.Contains(body, "Owner airport") {
		t.Fatalf("taken down source should leave catalog: %s", body)
	}
	if body = call("GET", "/api/v1/moderation/sources?status=takedown", "", mgr, 200).Body.String(); !strings.Contains(body, "Owner airport") || strings.Contains(body, "Admin airport") || !strings.Contains(body, `"takedowns":1`) || !strings.Contains(body, `"owner_request"`) {
		t.Fatalf("moderation list: %s", body)
	}
	if body = call("GET", "/api/v1/moderation/sources", "", mgr, 200).Body.String(); strings.Contains(body, "Admin airport") {
		t.Fatalf("manager must not list admin sources: %s", body)
	}
	call("GET", "/api/v1/sources/"+sid+"/moderation", "", other, 403)
	if body = call("GET", "/api/v1/sources/"+sid+"/moderation", "", owner, 200).Body.String(); !strings.Contains(body, `"action":"takedown"`) {
		t.Fatalf("history: %s", body)
	}
	waitMail := func(to, want string) {
		t.Helper()
		for i := 0; i < 50; i++ {
			mu.Lock()
			m, ok := mails[to]
			mu.Unlock()
			if ok && strings.Contains(m.Subject, want) {
				if !strings.Contains(m.HTML, "Owner airport") || !strings.Contains(m.HTML, "<!DOCTYPE html>") {
					t.Fatalf("mail html: %s", m.HTML)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("mail %q to %s not sent", want, to)
	}
	waitMail("owner@example.org", "来源已被下架")

	// 恢复后重新排队处理，提供方收到通知
	body = call("POST", "/api/v1/sources/"+sid+"/restore", `{}`, admin, 200).Body.String()
	if !strings.Contains(body, `"enabled":true`) || strings.Contains(body, `"takedown":{`) {
		t.Fatalf("restore: %s", body)
	}
	waitMail("owner@example.org", "来源已恢复上架")
	call("PATCH", "/api/v1/sources/"+sid, `{"enabled":false}`, owner, 200)
	call("PATCH", "/api/v1/sources/"+sid, `{"enabled":true}`, owner, 200)

	// 用户详情：统计、来源与第三方绑定，管理员可解绑
	if _, e = s.DB.Exec(`INSERT INTO np_user_identities(provider,subject,user_id,login,created_at) VALUES('github','42',?,'octo',UTC_TIMESTAMP(6))`, ownerID); e != nil {
		t.Fatal(e)
	}
	var detail pool.UserDetail
	_ = json.Unmarshal(call("GET", "/api/v1/users/"+id(ownerID)+"/detail", "", mgr, 200).Body.Bytes(), &detail)
	if detail.User.Username != "owner" || len(detail.Identities) != 1 || detail.Identities[0].Login != "octo" || detail.Stats.Sources != 1 || detail.Stats.SourcesActive != 1 || detail.Stats.Sessions < 1 || detail.Stats.LastLoginAt == nil || len(detail.Sources) != 1 {
		t.Fatalf("user detail: %+v", detail)
	}
	call("GET", "/api/v1/users/1/detail", "", mgr, 403)
	call("GET", "/api/v1/users/"+id(ownerID)+"/detail", "", other, 403)
	call("DELETE", "/api/v1/users/"+id(ownerID)+"/identities/github", "", other, 403)
	call("DELETE", "/api/v1/users/"+id(ownerID)+"/identities/github", "", mgr, 200)
	call("DELETE", "/api/v1/users/"+id(ownerID)+"/identities/github", "", mgr, 404)

	// 运行统计对普通用户和访客开放，但隐藏错误详情和进程信息
	if body = call("GET", "/api/v1/statistics", "", nil, 200).Body.String(); !strings.Contains(body, `"overview"`) || !strings.Contains(body, `"heap_bytes":0`) {
		t.Fatalf("guest statistics: %s", body)
	}
}
