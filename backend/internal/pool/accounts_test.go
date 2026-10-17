package pool

import (
	"strings"
	"testing"
)

func TestPasswordHashDoesNotTruncateLongPasswords(t *testing.T) {
	password := strings.Repeat("密", 30) + "tail-one"
	hash, e := HashPassword(password)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(hash, "$2") || !CheckPassword(hash, password) || CheckPassword(hash, strings.Repeat("密", 30)+"tail-two") {
		t.Fatal("bcrypt failed to preserve the full password")
	}
	other, e := HashPassword(password)
	if e != nil || other == hash {
		t.Fatal("password hashes must be randomly salted")
	}
	for _, invalid := range []string{"short", strings.Repeat("x", 1025)} {
		if _, e := HashPassword(invalid); e == nil {
			t.Fatal("invalid password length accepted")
		}
	}
}
