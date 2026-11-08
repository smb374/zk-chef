package version

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type semver struct {
	core [3]int
	pre  []string // pre-release identifiers; empty for a release
}

func parse(s string) (semver, error) {
	var v semver
	rest := strings.TrimPrefix(s, "v")
	rest, _, _ = strings.Cut(rest, "+") // build metadata never affects order
	rest, pre, hasPre := strings.Cut(rest, "-")
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return v, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", s)
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		if slices.Contains(v.pre, "") {
			return v, fmt.Errorf("%q has an empty pre-release identifier", s)
		}
	}
	return v, nil
}

// Valid reports whether s is a semantic version with an optional leading "v".
func Valid(s string) bool {
	_, err := parse(s)
	return err == nil
}

// Compare returns -1, 0 or 1 as semantic version a is lower than, equal to or higher than b.
// A leading "v" is optional. It returns an error when either value is not a semantic version.
func Compare(a, b string) (int, error) {
	va, err := parse(a)
	if err != nil {
		return 0, err
	}
	vb, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := range va.core {
		if c := cmpInt(va.core[i], vb.core[i]); c != 0 {
			return c, nil
		}
	}
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0, nil
	case len(va.pre) == 0:
		return 1, nil
	case len(vb.pre) == 0:
		return -1, nil
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		if c := cmpIdent(va.pre[i], vb.pre[i]); c != 0 {
			return c, nil
		}
	}
	return cmpInt(len(va.pre), len(vb.pre)), nil
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpIdent orders pre-release identifiers: numeric ones numerically and below alphanumeric ones.
func cmpIdent(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	switch {
	case ea == nil && eb == nil:
		return cmpInt(na, nb)
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}
