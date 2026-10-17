package pool_test

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/pool"
	"github.com/go-sql-driver/mysql"
)

const legacySecret = "legacy-test-secret"

func legacySeal(t *testing.T, plain string) string {
	t.Helper()
	key := sha256.Sum256([]byte(legacySecret))
	block, _ := aes.NewCipher(key[:])
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil))
}

// legacyDB 在测试库旁建立旧站结构的临时库，没有建库权限时跳过
func legacyDB(t *testing.T, name string, statements []string) *sql.DB {
	t.Helper()
	cfg, err := mysql.ParseDSN(os.Getenv("MYSQL_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ParseTime, cfg.Loc, cfg.MultiStatements = true, time.UTC, false
	root := *cfg
	root.DBName = ""
	admin, err := sql.Open("mysql", root.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	db := cfg.DBName + "_" + name
	if _, err = admin.Exec("DROP DATABASE IF EXISTS `" + db + "`"); err == nil {
		_, err = admin.Exec("CREATE DATABASE `" + db + "` CHARACTER SET utf8mb4")
	}
	if err != nil {
		t.Skip("no privilege to create legacy fixture database:", err)
	}
	t.Cleanup(func() {
		if a, e := sql.Open("mysql", root.FormatDSN()); e == nil {
			_, _ = a.Exec("DROP DATABASE IF EXISTS `" + db + "`")
			a.Close()
		}
	})
	cfg.DBName = db
	conn, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	for _, q := range statements {
		if _, err = conn.Exec(q); err != nil {
			t.Fatalf("%v: %s", err, q)
		}
	}
	return conn
}

func TestMySQLLegacyImport(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// 启用数据加密，验证导入的地址与密钥都被重新加密
	if err := pool.SetDataKey(strings.Repeat("cd", 32)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.SetDataKey("") })
	t.Cleanup(func() {
		// 恢复其他测试依赖的默认管理员
		_, _ = s.DB.Exec(`DELETE FROM np_users WHERE id<>1`)
		_, _ = s.DB.Exec(`UPDATE np_users SET username='admin',display_name='管理员',email=NULL,email_verified_at=NULL,enabled=1,role='admin',xp=0,points=0 WHERE id=1`)
		// 直接覆盖设置，避免加密后的旧密钥残留到其他测试
		raw, _ := json.Marshal(testDefaults())
		_, _ = s.DB.Exec(`UPDATE np_settings SET body=? WHERE id=1`, raw)
		_, _ = s.DB.Exec(`DELETE FROM np_sources`)
		_, _ = s.DB.Exec(`DELETE FROM np_nodes`)
	})
	body := `proxies:
 - {name: exit, type: ss, server: exit.example.com, port: 443, cipher: aes-128-gcm, password: a, dialer-proxy: relay}
 - {name: relay, type: ss, server: relay.example.com, port: 443, cipher: aes-128-gcm, password: b}
 - {name: plain, type: ss, server: plain.example.com, port: 443, cipher: aes-128-gcm, password: c}
`
	accounts := legacyDB(t, "legacy_accounts", []string{
		"CREATE TABLE users (id BIGINT PRIMARY KEY, public_id CHAR(36), email VARCHAR(255) NULL, name VARCHAR(80) NOT NULL, password_hash VARCHAR(255) NOT NULL, is_admin TINYINT NOT NULL DEFAULT 0, disabled_at DATETIME(3) NULL, created_at DATETIME(3) NOT NULL, username VARCHAR(20) NULL, points INT UNSIGNED NOT NULL DEFAULT 0, xp INT UNSIGNED NOT NULL DEFAULT 0)",
		"CREATE TABLE oauth_bindings (id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT, provider VARCHAR(32), provider_id VARCHAR(128), username VARCHAR(128) NOT NULL DEFAULT '', created_at DATETIME(3) NOT NULL)",
		"CREATE TABLE ledger_entries (id BIGINT PRIMARY KEY AUTO_INCREMENT, public_id CHAR(36), user_id BIGINT, kind VARCHAR(16), event_type VARCHAR(40), xp_delta INT, points_delta INT, reason VARCHAR(200) NOT NULL DEFAULT '', created_at DATETIME(3) NOT NULL)",
		"CREATE TABLE settings (k VARCHAR(64) PRIMARY KEY, v TEXT NOT NULL)",
		`INSERT INTO users VALUES
 (101,'00000000-0000-0000-0000-000000000101','Boss@Example.com','老板','$argon2id$x',1,NULL,'2026-01-01','boss',2,5),
 (102,'00000000-0000-0000-0000-000000000102',NULL,'助手','',1,NULL,'2026-01-02','helper',0,100),
 (103,'00000000-0000-0000-0000-000000000103',NULL,'',  '',0,NULL,'2026-01-03',NULL,0,0),
 (104,'00000000-0000-0000-0000-000000000104','gone@example.com','停用','$2a$x',0,'2026-02-01','2026-01-04','gone',0,0),
 (105,'00000000-0000-0000-0000-000000000105',NULL,'Bob','',0,NULL,'2026-01-05','bob',0,3)`,
		`INSERT INTO oauth_bindings(user_id,provider,provider_id,username,created_at) VALUES
 (103,'github','9001','octo','2026-01-03'),(105,'linuxdo','7','old','2026-01-05'),(105,'linuxdo','8','new','2026-03-01')`,
		`INSERT INTO ledger_entries(public_id,user_id,kind,event_type,xp_delta,points_delta,reason,created_at) VALUES
 ('l1',101,'credit','checkin',5,2,'签到','2026-02-01'),('l2',102,'credit','linuxdo_trust',100,0,'LINUX DO','2026-02-01'),
 ('l3',105,'credit','provide',3,4,'共享','2026-02-02'),('l4',105,'debit','debit',0,-1,'消费','2026-02-03')`,
		`INSERT INTO settings VALUES ('email_suffixes','qq.com
example.com'),('smtp_host','mail.example.com'),('smtp_port','465'),('smtp_tls','1'),('smtp_user','mailer'),('smtp_password','smtp-secret'),('smtp_from','noreply@example.com'),('smtp_from_name','MCII'),('github_client_id','gid'),('github_client_secret','gsecret'),('github_enabled','1')`,
	})
	proxy := legacyDB(t, "legacy_proxy", []string{
		"CREATE TABLE profiles (sso_id VARCHAR(64) PRIMARY KEY, picture VARCHAR(500) NOT NULL DEFAULT '', clash_wrap TINYINT NOT NULL DEFAULT 1, banned TINYINT NOT NULL DEFAULT 0, checkin_on DATE NULL, checkin_streak INT NOT NULL DEFAULT 0)",
		"CREATE TABLE subscriptions (id INT UNSIGNED PRIMARY KEY, owner_sso VARCHAR(64), name VARCHAR(80), max_claims INT UNSIGNED NOT NULL DEFAULT 0, status VARCHAR(20) NOT NULL DEFAULT 'live', body_yaml MEDIUMTEXT NULL, created_at DATETIME NOT NULL, min_level TINYINT UNSIGNED NOT NULL DEFAULT 0, sub_url_enc MEDIUMTEXT NULL, share_token CHAR(32) NULL, deleted_at DATETIME NULL, kind VARCHAR(20) NOT NULL DEFAULT 'url')",
		"CREATE TABLE usage_pulls (sso_id VARCHAR(64), subscription_id INT UNSIGNED, first_at DATETIME NOT NULL, last_at DATETIME NOT NULL)",
		"CREATE TABLE seats (sso_id VARCHAR(64) PRIMARY KEY, subscription_id INT UNSIGNED NULL, token CHAR(32) NOT NULL, claimed_at DATETIME NULL)",
		"CREATE TABLE records (id INT UNSIGNED PRIMARY KEY AUTO_INCREMENT, sso_id VARCHAR(64), action VARCHAR(20), created_at DATETIME NOT NULL, subscription_id INT UNSIGNED NULL)",
		"CREATE TABLE follows (follower_sso VARCHAR(64), owner_sso VARCHAR(64), created_at DATETIME NOT NULL, deleted_at DATETIME NULL)",
		`INSERT INTO profiles VALUES ('00000000-0000-0000-0000-000000000103','',1,0,'2026-10-16',3),('00000000-0000-0000-0000-000000000105','',0,0,NULL,0)`,
	})
	if _, err := proxy.Exec(`INSERT INTO subscriptions(id,owner_sso,name,max_claims,status,body_yaml,created_at,min_level,sub_url_enc,share_token,deleted_at) VALUES
 (1,'00000000-0000-0000-0000-000000000101','老板的来源',999999999,'live',?,'2026-03-01',0,?,'0123456789abcdef0123456789abcdef',NULL),
 (2,'00000000-0000-0000-0000-000000000102','草稿来源',5,'draft',?,'2026-03-02',2,?,NULL,NULL),
 (3,'00000000-0000-0000-0000-000000000102','已删除',0,'live',NULL,'2026-03-03',0,NULL,NULL,'2026-04-01'),
 (4,'00000000-0000-0000-0000-000000000105','无地址来源',0,'live',?,'2026-03-04',0,NULL,NULL,NULL)`,
		body, legacySeal(t, "https://up.example.com/sub?token=abc"), body, legacySeal(t, "https://draft.example.com/x"), body); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO seats VALUES ('00000000-0000-0000-0000-000000000103',1,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-10-10'),('00000000-0000-0000-0000-000000000105',2,'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','2026-10-11'),('00000000-0000-0000-0000-000000000104',NULL,'cccccccccccccccccccccccccccccccc',NULL)`,
		`INSERT INTO usage_pulls VALUES ('00000000-0000-0000-0000-000000000103',1,'2026-10-10','2026-10-15')`,
		`INSERT INTO records(sso_id,action,created_at,subscription_id) VALUES ('00000000-0000-0000-0000-000000000103','领取','2026-10-10',1),('00000000-0000-0000-0000-000000000105','领取','2026-10-09',1),('00000000-0000-0000-0000-000000000105','切换','2026-10-11',2),('00000000-0000-0000-0000-000000000101','封禁','2026-10-11',NULL)`,
		`INSERT INTO follows VALUES ('00000000-0000-0000-0000-000000000103','00000000-0000-0000-0000-000000000101','2026-05-01',NULL),('00000000-0000-0000-0000-000000000105','00000000-0000-0000-0000-000000000101','2026-05-01','2026-06-01')`,
	} {
		if _, err := proxy.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// 当前站点已有数据，导入后不保留
	stale := account(t, s, "stale", 10, 1, 1)
	opts := pool.LegacyOptions{Accounts: accounts, Proxy: proxy, SubSecret: legacySecret, AdminEmail: "boss@example.com", DryRun: true}
	plan, err := s.ImportLegacy(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Users != 5 || plan.Admin != "boss" || len(plan.Managers) != 1 || plan.GeneratedNames != 1 || plan.Sources != 3 || plan.SourcesSkipped != 1 || plan.Subscriptions != 2 || plan.Identities != 2 || len(plan.DupIdentities) != 1 || plan.Follows != 1 || plan.Events != 4 {
		t.Fatalf("plan: %+v", plan)
	}
	if _, err = s.User(ctx, stale.ID); err != nil {
		t.Fatal("dry run must not write", err)
	}
	// 试运行不需要目标库
	if offline, err := pool.NewStore(nil).ImportLegacy(ctx, opts); err != nil || offline.Users != plan.Users || offline.Subscriptions != plan.Subscriptions {
		t.Fatalf("offline dry run: %+v %v", offline, err)
	}
	noTarget := opts
	noTarget.DryRun = false
	if _, err = pool.NewStore(nil).ImportLegacy(ctx, noTarget); err == nil {
		t.Fatal("import without target accepted")
	}
	if _, err = s.ImportLegacy(ctx, pool.LegacyOptions{Accounts: accounts, Proxy: proxy, SubSecret: "wrong", AdminEmail: "boss@example.com", DryRun: true}); err == nil {
		t.Fatal("wrong secret accepted")
	}
	opts.DryRun = false
	if _, err = s.ImportLegacy(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if _, err = s.User(ctx, stale.ID); err == nil {
		t.Fatal("current data must be wiped")
	}
	boss, err := s.User(ctx, 1)
	if err != nil || boss.Username != "boss" || boss.Role != "admin" || boss.Email != "boss@example.com" || boss.XP != 5 {
		t.Fatalf("admin: %+v %v", boss, err)
	}
	helper, _ := s.User(ctx, 102)
	unnamed, _ := s.User(ctx, 103)
	gone, _ := s.User(ctx, 104)
	if helper.Role != "manager" || unnamed.Username != "user103" || unnamed.DisplayName != "user103" || unnamed.CheckinStreak != 3 || gone.Enabled {
		t.Fatalf("users: %+v %+v %+v", helper, unnamed, gone)
	}
	var hashes, idents, xp int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM np_users WHERE password_hash<>''`).Scan(&hashes)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM np_user_identities`).Scan(&idents)
	_ = s.DB.QueryRow(`SELECT COALESCE(SUM(xp),0) FROM np_ledger`).Scan(&xp)
	if hashes != 0 || idents != 2 || xp != 108 {
		t.Fatalf("hashes=%d identities=%d ledger xp=%d", hashes, idents, xp)
	}
	var login string
	_ = s.DB.QueryRow(`SELECT login FROM np_user_identities WHERE user_id=105`).Scan(&login)
	if login != "new" {
		t.Fatal("duplicate binding must keep the latest", login)
	}
	src, err := s.Source(ctx, 1)
	if err != nil || src.URL != "https://up.example.com/sub?token=abc" || !src.Enabled || src.MaxUsers != 0 || src.OwnerID != 1 || src.ShareToken != "0123456789abcdef0123456789abcdef" || src.NodeCount != 3 {
		t.Fatalf("source: %+v %v", src, err)
	}
	draft, _ := s.Source(ctx, 2)
	file, _ := s.Source(ctx, 4)
	if draft.Enabled || draft.MaxUsers != 5 || draft.MinLevel != 2 || file.Kind != "file" || file.NodeCount != 3 {
		t.Fatalf("draft/file: %+v %+v", draft, file)
	}
	if _, err = s.Source(ctx, 3); err == nil {
		t.Fatal("deleted source imported")
	}
	nodes, err := s.SubscriptionNodes(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || len(nodes) != 3 {
		t.Fatalf("legacy token export: %d %v", len(nodes), err)
	}
	raw, _, _, err := s.ExportSavedSubscription(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "clash")
	if err != nil || !strings.Contains(string(raw), "dialer-proxy") {
		t.Fatalf("chained export: %v", err)
	}
	subs, err := s.Subscriptions(pool.WithUser(ctx, unnamed))
	if err != nil || len(subs) != 1 || len(subs[0].FollowIDs) != 1 || subs[0].RuleProfileID == nil || *subs[0].RuleProfileID != 0 {
		t.Fatalf("legacy subscription: %+v %v", subs, err)
	}
	bob, _ := s.User(ctx, 105)
	bobSubs, _ := s.Subscriptions(pool.WithUser(ctx, bob))
	if len(bobSubs) != 1 || bobSubs[0].RuleProfileID != nil {
		t.Fatalf("clash_wrap=0 must export plain nodes: %+v", bobSubs)
	}
	var events, follows, exposures int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM np_source_events`).Scan(&events)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM np_follows`).Scan(&follows)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM np_source_exposures`).Scan(&exposures)
	if events != 4 || follows != 1 || exposures != 2 {
		t.Fatalf("events=%d follows=%d exposures=%d", events, follows, exposures)
	}
	settings, err := s.Settings(ctx)
	if err != nil || settings.Login.SMTP.Host != "mail.example.com" || settings.Login.SMTP.Security != "ssl" || !settings.Login.GitHub.Enabled || settings.Login.RequireEmail || len(settings.Login.EmailSuffixes) != 2 {
		t.Fatalf("login settings: %+v %v", settings.Login, err)
	}
	var sealed string
	_ = s.DB.QueryRow(`SELECT body FROM np_settings WHERE id=1`).Scan(&sealed)
	var sealedURL string
	_ = s.DB.QueryRow(`SELECT url FROM np_sources WHERE id=1`).Scan(&sealedURL)
	if strings.Contains(sealed, "smtp-secret") || strings.Contains(sealed, "gsecret") || strings.Contains(sealedURL, "up.example.com") {
		t.Fatal("legacy secrets stored in plaintext")
	}
}
