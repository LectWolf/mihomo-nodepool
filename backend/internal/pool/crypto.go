package pool

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
)

// 静态数据加密：订阅地址、原始订阅内容、节点配置与服务器地址使用 AES-256-GCM。
// 密文带 enc1: 前缀并绑定用途，避免不同字段之间互换密文。
const sealPrefix = "enc1:"

const (
	purposeSourceURL     = "np_sources.url"
	purposeSourceContent = "np_sources.content"
	purposeNodeConfig    = "np_nodes.config"
	purposeNodeServer    = "np_nodes.server"
)

var ErrDataKey = errors.New("数据库中存在加密数据，请配置正确的 NODEPOOL_DATA_KEY 后重启")

var dataAEAD atomic.Pointer[cipher.AEAD]

// SetDataKey accepts a 32-byte key encoded as Base64 or 64 hex characters.
func SetDataKey(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		dataAEAD.Store(nil)
		return nil
	}
	var key []byte
	if len(raw) == 64 {
		key, _ = hex.DecodeString(raw)
	}
	if key == nil {
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, err := enc.DecodeString(raw); err == nil {
				key = b
				break
			}
		}
	}
	if len(key) != 32 {
		return errors.New("NODEPOOL_DATA_KEY 必须是 32 字节密钥的 Base64 或 64 位十六进制")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	dataAEAD.Store(&aead)
	return nil
}

func DataEncryptionEnabled() bool { return dataAEAD.Load() != nil }

func sealed(v string) bool { return strings.HasPrefix(v, sealPrefix) }

func seal(purpose, plain string) (string, error) {
	p := dataAEAD.Load()
	if p == nil || plain == "" || sealed(plain) {
		return plain, nil
	}
	aead := *p
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := aead.Seal(nonce, nonce, []byte(plain), []byte(purpose))
	return sealPrefix + base64.RawStdEncoding.EncodeToString(out), nil
}

func unseal(purpose, v string) (string, error) {
	if !sealed(v) {
		return v, nil
	}
	p := dataAEAD.Load()
	if p == nil {
		return "", ErrDataKey
	}
	aead := *p
	raw, err := base64.RawStdEncoding.DecodeString(v[len(sealPrefix):])
	if err != nil || len(raw) < aead.NonceSize() {
		return "", ErrDataKey
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(purpose))
	if err != nil {
		return "", ErrDataKey
	}
	return string(plain), nil
}

// 节点配置列为 JSON 类型，密文以 JSON 字符串保存
func sealConfig(raw []byte) ([]byte, error) {
	if !DataEncryptionEnabled() || len(raw) == 0 || raw[0] == '"' {
		return raw, nil
	}
	v, err := seal(purposeNodeConfig, string(raw))
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func unsealConfig(raw []byte) ([]byte, error) {
	if len(raw) == 0 || raw[0] != '"' {
		return raw, nil
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil || !sealed(v) {
		return raw, nil
	}
	plain, err := unseal(purposeNodeConfig, v)
	return []byte(plain), err
}

// ensureEncryption migrates plaintext rows when a key is configured and
// refuses to start without one once encrypted rows exist.
func (s *Store) ensureEncryption(ctx context.Context) error {
	var width int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(CHARACTER_MAXIMUM_LENGTH),0) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='np_nodes' AND COLUMN_NAME='server'`).Scan(&width); err != nil {
		return err
	}
	// 密文比明文长，服务器地址列需要放宽
	if width > 0 && width < 1024 {
		if _, err := s.DB.ExecContext(ctx, `ALTER TABLE np_nodes MODIFY server VARCHAR(1024) NOT NULL`); err != nil {
			return err
		}
	}
	if err := s.stripArchiveServers(ctx); err != nil {
		return err
	}
	if !DataEncryptionEnabled() {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM np_sources WHERE url LIKE 'enc1:%' OR content LIKE 'enc1:%')+(SELECT COUNT(*) FROM np_nodes WHERE server LIKE 'enc1:%' OR JSON_TYPE(config)='STRING')`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrDataKey
		}
		return nil
	}
	// 用已有密文校验密钥，避免换错密钥后混写两种密文
	for _, check := range []struct{ query, purpose string }{
		{`SELECT url FROM np_sources WHERE url LIKE 'enc1:%' LIMIT 1`, purposeSourceURL},
		{`SELECT server FROM np_nodes WHERE server LIKE 'enc1:%' LIMIT 1`, purposeNodeServer},
	} {
		var sample string
		err := s.DB.QueryRowContext(ctx, check.query).Scan(&sample)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err = unseal(check.purpose, sample); err != nil {
			return ErrDataKey
		}
	}
	for {
		rows, err := s.DB.QueryContext(ctx, `SELECT id,url,content FROM np_sources WHERE (url<>'' AND url NOT LIKE 'enc1:%') OR (content<>'' AND content NOT LIKE 'enc1:%') ORDER BY id LIMIT 100`)
		if err != nil {
			return err
		}
		type row struct {
			id           int64
			url, content string
		}
		batch := []row{}
		for rows.Next() {
			var r row
			if err = rows.Scan(&r.id, &r.url, &r.content); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			u, err := seal(purposeSourceURL, r.url)
			if err != nil {
				return err
			}
			c, err := seal(purposeSourceContent, r.content)
			if err != nil {
				return err
			}
			if _, err = s.DB.ExecContext(ctx, `UPDATE np_sources SET url=?,content=? WHERE id=?`, u, c, r.id); err != nil {
				return err
			}
		}
	}
	for {
		rows, err := s.DB.QueryContext(ctx, `SELECT id,server,config FROM np_nodes WHERE JSON_TYPE(config)<>'STRING' OR (server<>'' AND server NOT LIKE 'enc1:%') ORDER BY id LIMIT 500`)
		if err != nil {
			return err
		}
		type row struct {
			id     int64
			server string
			config []byte
		}
		batch := []row{}
		for rows.Next() {
			var r row
			if err = rows.Scan(&r.id, &r.server, &r.config); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			server, err := seal(purposeNodeServer, r.server)
			if err != nil {
				return err
			}
			config, err := sealConfig(r.config)
			if err != nil {
				return err
			}
			if _, err = s.DB.ExecContext(ctx, `UPDATE np_nodes SET server=?,config=? WHERE id=?`, server, config, r.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// 旧版历史快照含服务器地址，启动时清除
func (s *Store) stripArchiveServers(ctx context.Context) error {
	var last int64
	for {
		rows, err := s.DB.QueryContext(ctx, `SELECT id,node_details FROM np_subscription_archives WHERE id>? AND node_details LIKE '%"server":"_%' ORDER BY id LIMIT 200`, last)
		if err != nil {
			return err
		}
		type row struct {
			id  int64
			raw []byte
		}
		batch := []row{}
		for rows.Next() {
			var r row
			if err = rows.Scan(&r.id, &r.raw); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, r := range batch {
			last = r.id
			var details []map[string]any
			if json.Unmarshal(r.raw, &details) != nil {
				continue
			}
			for _, d := range details {
				d["server"], d["port"] = "", 0
			}
			raw, err := json.Marshal(details)
			if err != nil {
				return err
			}
			if _, err = s.DB.ExecContext(ctx, `UPDATE np_subscription_archives SET node_details=? WHERE id=?`, raw, r.id); err != nil {
				return err
			}
		}
	}
}
