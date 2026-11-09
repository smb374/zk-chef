// Package version holds the binary version.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// Version is set at link time: -ldflags "-X github.com/smb374/zk-chef/internal/version.Version=v1.2.3".
var Version string

// ReadBuildInfo is a seam for tests.
var ReadBuildInfo = debug.ReadBuildInfo

// pseudoVersion matches the end of a Go pseudo-version: -yyyymmddhhmmss-abcdefabcdef, or
// .yyyymmddhhmmss-abcdefabcdef after a pre-release part.
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}$`)

// Current returns the ldflags version, else the build-info module version, else "dev".
// A build-info version that is a pseudo-version or ends in "+dirty" is a local build: "dev".
func Current() string {
	if Version != "" {
		return Version
	}
	if bi, ok := ReadBuildInfo(); ok {
		v := bi.Main.Version
		if v != "" && v != "(devel)" && !strings.HasSuffix(v, "+dirty") && !pseudoVersion.MatchString(v) {
			return v
		}
	}
	return "dev"
}
