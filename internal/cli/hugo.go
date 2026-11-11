package cli

import (
	"os/exec"
	"regexp"

	"github.com/smb374/zk-chef/internal/version"
)

// hugoFloor is the oldest Hugo the Export scaffold builds with. Keep it equal to the module minimum in export-scaffold/hugo/hugo.toml.tmpl and to the guard in layouts/baseof.html.
const hugoFloor = "0.169.0"

var hugoVersionOutput = regexp.MustCompile(`^hugo v(\d+\.\d+\.\d+)`)

// installedHugo returns the MAJOR.MINOR.PATCH of the hugo on PATH, read from `hugo version`. ok is false when hugo is missing or its output has no such version.
func installedHugo() (v string, ok bool) {
	out, err := exec.Command("hugo", "version").Output()
	if err != nil {
		return "", false
	}
	m := hugoVersionOutput.FindSubmatch(out)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// hugoBelowFloor reports whether v is older than hugoFloor.
func hugoBelowFloor(v string) bool {
	c, err := version.Compare(v, hugoFloor)
	return err == nil && c < 0
}
