package update

import (
	"errors"
	"strings"
)

func parseStable(v string) (semver, bool) {
	if len(v) < 6 || v[0] != 'v' {
		return semver{}, false
	}
	parts := strings.Split(v[1:], ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var nums [3]int
	for i, part := range parts {
		if part == "" || len(part) > 9 {
			return semver{}, false
		}
		n := 0
		for _, c := range part {
			if c < '0' || c > '9' {
				return semver{}, false
			}
			n = n*10 + int(c-'0')
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2]}, true
}

type semver struct {
	major int
	minor int
	patch int
}

func compareSemver(a, b semver) int {
	if c := cmpInt(a.major, b.major); c != 0 {
		return c
	}
	if c := cmpInt(a.minor, b.minor); c != 0 {
		return c
	}
	return cmpInt(a.patch, b.patch)
}

func compareVersions(a, b string) (int, bool) {
	av, aok := parseStable(a)
	bv, bok := parseStable(b)
	if !aok || !bok {
		return 0, false
	}
	return compareSemver(av, bv), true
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func normalizeRepo(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || strings.Contains(name, "/") || !repoSegment(owner) || !repoSegment(name) {
		return "", errors.New("仓库名无效")
	}
	return owner + "/" + name, nil
}

func repoSegment(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 100 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}
