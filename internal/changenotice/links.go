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
	case strings.HasPrefix(remote, "/") || strings.HasPrefix(remote, "."):
		return WebRepository{}, false
	case strings.Contains(remote, ":"):
		// scp-style user@host:owner/name
		rest := remote[strings.LastIndex(remote, "@")+1:]
		colon := strings.Index(rest, ":")
		if colon <= 0 {
			return WebRepository{}, false
		}
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

// Links returns where a person reads the detail of a change. For a range
// that moved forward (linear), that is each pull request when there are few
// enough, plus the compare view of the whole range, since commits made
// outside a pull request are part of it too. When the two revisions diverged,
// a compare view would start from their merge base and not match the
// summary, so both commits are linked instead. Without a web repository
// there are no links.
func Links(repo WebRepository, ok bool, from, to string, prs []int, linear bool) []string {
	if !ok || to == "" {
		return nil
	}
	if from == "" {
		return []string{repo.base() + "/commit/" + to}
	}
	if !linear {
		return []string{repo.base() + "/commit/" + from, repo.base() + "/commit/" + to}
	}
	var out []string
	if len(prs) <= maxLinks {
		for _, n := range prs {
			out = append(out, fmt.Sprintf("%s/pull/%d", repo.base(), n))
		}
	}
	return append(out, repo.base()+"/compare/"+from+"..."+to)
}
