// Package ghauth diagnoses GitHub CLI authentication for private GitHub repos.
package ghauth

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// Runner executes external commands. Tests can replace it.
type Runner func(name string, args ...string) ([]byte, error)

// CheckGitURL verifies gh auth for HTTPS GitHub URLs.
func CheckGitURL(gitURL string, runner Runner) error {
	host := githubHost(gitURL)
	if host == "" {
		return nil
	}
	if runner == nil {
		if _, err := exec.LookPath("gh"); err != nil {
			return fmt.Errorf("GitHub auth required for %s; install GitHub CLI and run `gh auth login`: %w", gitURL, err)
		}
		runner = execCommand
	}
	out, err := runner("gh", "auth", "status", "--hostname", host)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("GitHub auth required for %s; run `gh auth login`: %s", gitURL, msg)
	}
	return nil
}

func githubHost(gitURL string) string {
	parsed, err := url.Parse(gitURL)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "https" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "github.com" {
		return host
	}
	return ""
}

func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
