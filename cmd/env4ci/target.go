package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/gitinfo"
	"github.com/bakhod1r/env4ci/internal/infrastructure/provider/github"
	"github.com/bakhod1r/env4ci/internal/infrastructure/provider/gitlab"
	"github.com/bakhod1r/env4ci/internal/infrastructure/provider/vault"
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
	Provider    string // github | gitlab | vault
	Repo        string
	BaseURL     string
	Environment string // "" = repository level / scope "*"
	File        string
	Protected   bool
	Vault       *config.Vault // vault only; Repo holds the resolved path

	from []string // where each value came from, for Describe
}

// Describe prints one context line so the user sees what will be touched.
func (t target) Describe() string {
	env := "repository level"
	switch t.Provider {
	case "gitlab":
		env = `scope "*"`
	case "vault":
		env = "shared"
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

	// Provider: argument, the only configured target, or git remote.
	configured := cfg.Targets.Names()
	switch {
	case len(pos) == 1:
		t.Provider = pos[0]
	case len(configured) == 1:
		t.Provider = configured[0]
	case len(configured) > 1:
		return t, fmt.Errorf("several targets configured (%s): name one, e.g. env4ci push %s", strings.Join(configured, ", "), configured[0])
	case remoteErr == nil && remote.Provider != "":
		t.Provider = remote.Provider
		t.from = append(t.from, "provider from git remote")
	default:
		return t, errors.New("provider required (github, gitlab or vault); could not infer it from env4ci.yaml or git remote origin")
	}
	if t.Provider != "github" && t.Provider != "gitlab" && t.Provider != "vault" {
		return t, fmt.Errorf("unknown provider %q (want github, gitlab or vault)", t.Provider)
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
	case "vault":
		v := cfg.Targets.Vault
		if v == nil {
			v = &config.Vault{}
		}
		vc := *v
		vc.Address = first(vc.Address, os.Getenv("VAULT_ADDR"))
		vc.Namespace = first(vc.Namespace, os.Getenv("VAULT_NAMESPACE"))
		if vc.Address == "" {
			return t, errors.New("vault: address unknown (targets.vault.address or VAULT_ADDR)")
		}
		if vc.Path == "" {
			return t, errors.New("vault: targets.vault.path is required, e.g. myapp/{env}")
		}
		t.Vault = &vc
		cfgRepo = vc.Path
	}
	t.Repo = first(o.repo, cfgRepo)
	t.BaseURL = cfgBase
	if t.Provider == "vault" {
		remoteMatches = false
	}
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

	env, file, from, err := resolveEnvFile(cfg, o, git, cfgEnv)
	if err != nil {
		return t, err
	}
	t.Environment, t.File = env, file
	if from != "" {
		t.from = append(t.from, from)
	}

	if t.Provider == "vault" {
		t.Repo = vaultPath(t.Repo, t.Environment)
	}
	return t, nil
}

// resolveEnvFile picks the environment (-e, --shared, branch map, then
// fallback) and its local file (-f, environments map, source, .env).
// It needs no provider, so commands like verify can use it directly.
func resolveEnvFile(cfg config.Config, o opts, git gitReader, fallback string) (env, file, from string, err error) {
	switch {
	case o.env != "":
		env = o.env
	case o.shared:
	case len(cfg.Branches) > 0:
		branch, err := git.Branch()
		if err != nil {
			return "", "", "", fmt.Errorf("branches: is configured but the current branch is unknown: %w", err)
		}
		e, ok := domain.BranchMap(cfg.Branches).Environment(branch)
		if !ok {
			return "", "", "", fmt.Errorf("branch %q has no environment in branches: (use -e <env>, --shared, or add a mapping)", branch)
		}
		env, from = e, "branch "+branch
	default:
		env = fallback
	}
	return env, first(o.file, cfg.Environments[env], cfg.Source, ".env"), from, nil
}

// vaultPath fills {env}; repository level becomes "shared". A path without
// {env} gets the environment appended so environments never share a secret.
func vaultPath(tmpl, env string) string {
	name := env
	if name == "" {
		name = "shared"
	}
	if strings.Contains(tmpl, "{env}") {
		return strings.ReplaceAll(tmpl, "{env}", name)
	}
	if env == "" {
		return tmpl
	}
	return strings.TrimRight(tmpl, "/") + "/" + env
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
	case "vault":
		token := os.Getenv("VAULT_TOKEN")
		if token == "" {
			token = vaultTokenFile()
		}
		if token == "" {
			return nil, errors.New("vault: no token (set VAULT_TOKEN or run \"vault login\")")
		}
		return &vault.Client{Address: t.Vault.Address, Namespace: t.Vault.Namespace, Mount: t.Vault.Mount, Path: t.Repo, Token: token}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", t.Provider)
}

// vaultTokenFile reads ~/.vault-token, written by "vault login".
func vaultTokenFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".vault-token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
