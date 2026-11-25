package changenotice

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// maxLinks bounds the pull request links in one notice; past it the compare
// view covers the whole range.
const maxLinks = 8

// WebRepository is a manifest remote that has browsable pull requests and
// commits. Only GitHub remotes are recognized.
type WebRepository struct {
	Host  string
	Owner string
	Name  string
}

func (r WebRepository) base() string {
	return "https://" + r.Host + "/" + r.Owner + "/" + r.Name
}

// ParseRemote recognizes https, ssh and scp-style GitHub remotes.
func ParseRemote(remote string) (WebRepository, bool) {
	remote = strings.TrimSpace(remote)
	var host, path string
	switch {
	case strings.Contains(remote, "://"):
		u, err := url.Parse(remote)
		if err != nil {
			return WebRepository{}, false
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(remote, ":"):
		at := strings.LastIndex(remote, "@")
		rest := remote[at+1:]
		colon := strings.Index(rest, ":")
		host, path = rest[:colon], rest[colon+1:]
	default:
		return WebRepository{}, false
	}
	if !strings.EqualFold(host, "github.com") {
		return WebRepository{}, false
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return WebRepository{}, false
	}
	return WebRepository{Host: "github.com", Owner: parts[0], Name: parts[1]}, true
}

var (
	mergeSubject  = regexp.MustCompile(`^Merge pull request #([0-9]+)\b`)
	squashSubject = regexp.MustCompile(`\(#([0-9]+)\)\s*$`)
)

// PullRequests extracts pull request numbers from commit subjects, oldest
// first, each once.
func PullRequests(subjects []string) []int {
	seen := map[int]bool{}
	var out []int
	for i := len(subjects) - 1; i >= 0; i-- {
		m := mergeSubject.FindStringSubmatch(subjects[i])
		if m == nil {
			m = squashSubject.FindStringSubmatch(subjects[i])
		}
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// Links returns where a person reads the detail of a change: its pull
// requests when they are few enough, otherwise (or when there are none) the
// compare view for the range. Without a web repository there are no links.
func Links(repo WebRepository, ok bool, from, to string, prs []int) []string {
	if !ok || to == "" {
		return nil
	}
	if len(prs) > 0 && len(prs) <= maxLinks {
		out := make([]string, 0, len(prs))
		for _, n := range prs {
			out = append(out, fmt.Sprintf("%s/pull/%d", repo.base(), n))
		}
		return out
	}
	if from == "" {
		return []string{repo.base() + "/commit/" + to}
	}
	return []string{repo.base() + "/compare/" + from + "..." + to}
}
