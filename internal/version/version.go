// Package version exposes the build version stamped into the my binary.
package version

// Version is the my version. Local builds keep this source-tree default;
// goreleaser overrides it for tagged releases via -ldflags
// "-X github.com/fluxinc/my-cli/internal/version.Version={{.Version}}".
var Version = "0.1.0"
