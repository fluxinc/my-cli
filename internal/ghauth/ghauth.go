// Package ghauth preserves the legacy authentication-check API while routing
// every GitHub transport through the permission-aware access resolver.
package ghauth

import "github.com/fluxinc/my-cli/internal/access"

// Runner executes external commands. Tests can replace it.
type Runner func(name string, args ...string) ([]byte, error)

// CheckGitURL requires a positive read decision for HTTPS and SSH GitHub
// remotes. Non-GitHub and local remotes are unchanged.
func CheckGitURL(gitURL string, runner Runner) error {
	_, _, err := access.RequireGitHubPermission(gitURL, access.PermissionRead, access.Runner(runner))
	return err
}
