package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/gitinfo"
	"github.com/bakhod1r/env4ci/internal/infrastructure/provider/github"
	"github.com/bakhod1r/env4ci/internal/infrastructure/provider/gitlab"
)

// gitReader is what target resolution needs from git; faked in tests.
type gitReader interface {
	Remote() (gitinfo.Remote, error)
	Branch() (string, error)
}

type gitSource struct{ dir string }

func (g gitSource) Remote() (gitinfo.Remote, error) { return gitinfo.ReadRemote(g.dir) }
func (g gitSource) Branch() (string, error)         { return gitinfo.CurrentBranch(g.dir) }

// target is the fully resolved destination of a diff/push/pull.
type target struct {
	Provider    string // github | gitlab
	Repo        string
	BaseURL     string
	Environment string // "" = repository level / scope "*"
	File        string
	Protected   bool

	from []string // where each value came from, for Describe
}

// Describe prints one context line so the user sees what will be touched.
func (t target) Describe() string {
	env := "repository level"
	if t.Provider == "gitlab" {
		env = `scope "*"`
	}
	if t.Environment != "" {
		env = "environment " + t.Environment
	}
	line := fmt.Sprintf("→ %s %s · %s · %s", t.Provider, t.Repo, env, t.File)
	if len(t.from) > 0 {
		line += "  (" + strings.Join(t.from, ", ") + ")"
	}
	return line
}

// resolveTarget fills provider, repo, environment and file from, in order:
// flags, env4ci.yaml, then git (remote origin, current branch).
func resolveTarget(cfg config.Config, o opts, pos []string, git gitReader) (target, error) {
	var t target
	remote, remoteErr := git.Remote()

	// Provider.
	switch {
	case len(pos) == 1:
		t.Provider = pos[0]
	case remoteErr == nil && remote.Provider != "":
		t.Provider = remote.Provider
		t.from = append(t.from, "provider from git remote")
	default:
		return t, errors.New("provider required (github or gitlab); could not infer it from git remote origin")
	}
	if t.Provider != "github" && t.Provider != "gitlab" {
		return t, fmt.Errorf("unknown provider %q (want github or gitlab)", t.Provider)
	}
	remoteMatches := remoteErr == nil && remote.Provider == t.Provider

	// Repo and base URL.
	var cfgRepo, cfgEnv, cfgBase string
	switch t.Provider {
	case "github":
		if g := cfg.Targets.GitHub; g != nil {
			cfgRepo, cfgEnv, cfgBase = g.Repo, g.Environment, g.BaseURL
		}
	case "gitlab":
		if g := cfg.Targets.GitLab; g != nil {
			cfgRepo, cfgEnv, cfgBase, t.Protected = g.Project, g.Environment, g.BaseURL, g.Protected
		}
		cfgBase = first(cfgBase, os.Getenv("GITLAB_URL"), os.Getenv("CI_SERVER_URL"))
	}
	t.Repo = first(o.repo, cfgRepo)
	t.BaseURL = cfgBase
	if t.Repo == "" && remoteMatches {
		t.Repo = remote.Path
		t.from = append(t.from, "repo from git remote")
	}
	if t.BaseURL == "" && remoteMatches {
		t.BaseURL = remote.BaseURL()
	}
	if t.Repo == "" {
		return t, fmt.Errorf("%s: repository unknown (use --repo, set it in env4ci.yaml, or run inside a clone with origin on %s)", t.Provider, t.Provider)
	}

	// Environment: -e, --shared, branch map, config.
	switch {
	case o.env != "":
		t.Environment = o.env
	case o.shared:
		t.Environment = ""
	case len(cfg.Branches) > 0:
		branch, err := git.Branch()
		if err != nil {
			return t, fmt.Errorf("branches: is configured but the current branch is unknown: %w", err)
		}
		env, ok := domain.BranchMap(cfg.Branches).Environment(branch)
		if !ok {
			return t, fmt.Errorf("branch %q has no environment in branches: (use -e <env>, --shared, or add a mapping)", branch)
		}
		t.Environment = env
		t.from = append(t.from, "branch "+branch)
	default:
		t.Environment = cfgEnv
	}

	// Local file: -f, environments map, source, .env.
	t.File = first(o.file, cfg.Environments[t.Environment], cfg.Source, ".env")
	return t, nil
}

func newProvider(t target) (application.Provider, error) {
	switch t.Provider {
	case "github":
		c := &github.Client{BaseURL: t.BaseURL, Repo: t.Repo, Environment: t.Environment}
		c.Token = first(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))
		if c.Token == "" {
			c.Token = ghCLIToken()
		}
		if c.Token == "" {
			return nil, errors.New("github: no token (set GITHUB_TOKEN or run \"gh auth login\")")
		}
		return c, nil
	case "gitlab":
		c := &gitlab.Client{BaseURL: t.BaseURL, Project: t.Repo, Environment: t.Environment, Protected: t.Protected}
		c.Token = os.Getenv("GITLAB_TOKEN")
		if c.Token == "" {
			return nil, errors.New("gitlab: GITLAB_TOKEN not set")
		}
		return c, nil
	}
	return nil, fmt.Errorf("unknown provider %q", t.Provider)
}
