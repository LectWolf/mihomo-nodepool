package config

import "testing"

func TestLoadRejectsPartialDatabaseConfiguration(t *testing.T) {
	clearConfig(t)
	t.Setenv("MYSQL_HOST", "database.example")
	if _, err := Load(); err == nil {
		t.Fatal("partial database configuration must fail")
	}
}

func TestLoadPreservesPasswordAndRejectsInvalidPort(t *testing.T) {
	clearConfig(t)
	t.Setenv("MYSQL_HOST", "database.example")
	t.Setenv("MYSQL_USER", "nodepool")
	t.Setenv("MYSQL_DATABASE", "nodepool")
	t.Setenv("MYSQL_PASSWORD", " test-password ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MySQL.Password != " test-password " || !cfg.MySQL.Configured() {
		t.Fatal("database configuration or password was changed")
	}
	t.Setenv("MYSQL_PORT", "70000")
	if _, err := Load(); err == nil {
		t.Fatal("out-of-range port must fail")
	}
}

func clearConfig(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_ADDR", "DATA_DIR", "MYSQL_HOST", "MYSQL_PORT", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE", "MYSQL_TLS"} {
		t.Setenv(key, "")
	}
}
