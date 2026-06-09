// Package version exposes the build version stamped into the our binary.
package version

// Version is the our version. Local builds keep this source-tree default;
// goreleaser overrides it for tagged releases via -ldflags
// "-X github.com/fluxinc/our-ai/internal/version.Version={{.Version}}".
var Version = "0.1.0"
