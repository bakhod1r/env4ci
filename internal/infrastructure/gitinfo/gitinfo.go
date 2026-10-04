// Package gitinfo reads the repository and branch from the local git
// checkout (or CI environment), so commands work without --repo / -e.
package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Remote is a parsed git remote URL.
type Remote struct {
	Host     string // github.com, gitlab.example.com
	Path     string // owner/name or group/sub/project
	Provider string // "github", "gitlab", or "" when the host is not recognisable
}

// BaseURL is the API base for self-hosted hosts; empty for github.com / gitlab.com.
func (r Remote) BaseURL() string {
	switch {
	case r.Host == "github.com" || r.Host == "gitlab.com":
		return ""
	case r.Provider == "github":
		return "https://" + r.Host + "/api/v3"
	case r.Provider == "gitlab":
		return "https://" + r.Host
	}
	return ""
}

// ParseRemote understands https, ssh:// and scp-like (git@host:path) URLs.
func ParseRemote(raw string) (Remote, error) {
	raw = strings.TrimSpace(raw)
	var host, path string
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return Remote{}, fmt.Errorf("parse remote %q: %w", raw, err)
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(raw, "@") && strings.Contains(raw, ":"): // git@host:owner/repo
		at := strings.Index(raw, "@")
		rest := raw[at+1:]
		colon := strings.Index(rest, ":")
		host, path = rest[:colon], rest[colon+1:]
	default:
		return Remote{}, fmt.Errorf("unsupported remote URL %q", raw)
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || strings.Count(path, "/") < 1 {
		return Remote{}, fmt.Errorf("remote %q has no owner/repository path", raw)
	}
	return Remote{Host: host, Path: path, Provider: providerFor(host)}, nil
}

func providerFor(host string) string {
	h := strings.ToLower(host)
	switch {
	case strings.Contains(h, "github"):
		return "github"
	case strings.Contains(h, "gitlab"):
		return "gitlab"
	}
	return ""
}

// ReadRemote returns the parsed URL of remote "origin" for the repo containing dir.
func ReadRemote(dir string) (Remote, error) {
	out, err := git(dir, "remote", "get-url", "origin")
	if err != nil {
		return Remote{}, err
	}
	return ParseRemote(out)
}

// ciBranchVars are checked in order; PR/MR source branch wins over ref name.
var ciBranchVars = []string{"GITHUB_HEAD_REF", "GITHUB_REF_NAME", "CI_MERGE_REQUEST_SOURCE_BRANCH_NAME", "CI_COMMIT_BRANCH"}

func branchFromCI() string {
	for _, k := range ciBranchVars {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// CurrentBranch prefers CI variables (CI checkouts are often detached),
// then the local checkout.
func CurrentBranch(dir string) (string, error) {
	if b := branchFromCI(); b != "" {
		return b, nil
	}
	b, err := git(dir, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	if b == "" {
		return "", errors.New("git: detached HEAD, no current branch (use -e)")
	}
	return b, nil
}

func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- fixed binary, args from this package only
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
