// Package probe parses the local go toolchain version, used by the check preflight to warn on an
// unsupported Go.
package probe

import (
	"strconv"
	"strings"
)

// GoMinor parses the minor version out of `go version go1.26.4 ...`.
func GoMinor(goVersion string) int {
	i := strings.Index(goVersion, "go1.")
	if i < 0 {
		return 0
	}
	rest := goVersion[i+len("go1."):]
	end := strings.IndexAny(rest, ". ")
	if end < 0 {
		return 0
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}
