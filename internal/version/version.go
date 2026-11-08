// Package version holds the binary version.
package version

import "runtime/debug"

// Version is set at link time: -ldflags "-X github.com/smb374/zk-chef/internal/version.Version=v1.2.3".
var Version string

// Current returns the ldflags version, else the build-info module version, else "dev".
func Current() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
