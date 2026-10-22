// Package version exposes the build version stamped into the my binary.
package version

import "runtime/debug"

// Version is the my version. Local builds keep this source-tree default;
// goreleaser overrides it for tagged releases via -ldflags
// "-X github.com/fluxinc/my-cli/internal/version.Version={{.Version}}".
var Version = "0.1.0"

// sourceDefault is the unstamped value of Version. Every release that can
// read this code is newer than 0.1.0, so a binary reporting it was built
// from source without a version stamp.
const sourceDefault = "0.1.0"

// Current returns the running my version and whether it is a real build
// stamp. Go module build info (tagged or pseudo-version) wins over Version,
// matching what my --version prints.
func Current() (string, bool) {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version, true
	}
	return Version, Version != sourceDefault
}
