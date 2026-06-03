// Package version exposes the build version stamped into the flux binary.
package version

// Version is the flux version. Local builds keep this source-tree default;
// goreleaser overrides it for tagged releases via -ldflags
// "-X github.com/fluxinc/flux/internal/version.Version={{.Version}}".
var Version = "0.1.0"
