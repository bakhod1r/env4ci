// Package config loads env4ci.yaml. Tokens are never read from this file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/bakhod1r/env4ci/internal/domain"
)

const DefaultFile = "env4ci.yaml"

type Config struct {
	Source  string  `yaml:"source"`
	Default string  `yaml:"default"` // kind for unmatched keys
	Rules   []Rule  `yaml:"rules"`
	Targets Targets `yaml:"targets"`

	// Branches maps git branches (or globs) to CI environments.
	Branches map[string]string `yaml:"branches"`
	// Environments maps an environment to its local .env file.
	Environments map[string]string `yaml:"environments"`
	// Checks configures credential verification before push.
	Checks Checks `yaml:"checks"`
}

// Checks name the variables that hold each credential. Each field is a
// variable name from the .env file, not a value.
type Checks struct {
	SSH      []SSHCheck      `yaml:"ssh"`
	Registry []RegistryCheck `yaml:"registry"`
}

type SSHCheck struct {
	Key        string `yaml:"key"`
	Host       string `yaml:"host"`
	User       string `yaml:"user"`
	Port       string `yaml:"port"`
	KnownHosts string `yaml:"known_hosts"`
}

type RegistryCheck struct {
	Registry string `yaml:"registry"` // literal host, e.g. ghcr.io
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type Rule struct {
	Pattern string `yaml:"pattern"`
	Type    string `yaml:"type"`
}

type Targets struct {
	GitHub *GitHub `yaml:"github"`
	GitLab *GitLab `yaml:"gitlab"`
	Vault  *Vault  `yaml:"vault"`
}

// Names lists configured targets in a fixed order.
func (t Targets) Names() []string {
	var out []string
	if t.GitHub != nil {
		out = append(out, "github")
	}
	if t.GitLab != nil {
		out = append(out, "gitlab")
	}
	if t.Vault != nil {
		out = append(out, "vault")
	}
	return out
}

// Vault is a HashiCorp Vault KV v2 target. Path may contain {env}, replaced
// by the environment name ("shared" for repository level).
type Vault struct {
	Address   string `yaml:"address"` // else VAULT_ADDR
	Namespace string `yaml:"namespace"`
	Mount     string `yaml:"mount"` // default "secret"
	Path      string `yaml:"path"`  // e.g. myapp/{env}
}

type GitHub struct {
	Repo        string `yaml:"repo"`
	Environment string `yaml:"environment"`
	BaseURL     string `yaml:"base_url"`
}

type GitLab struct {
	Project     string `yaml:"project"`
	Environment string `yaml:"environment"`
	Protected   bool   `yaml:"protected"`
	BaseURL     string `yaml:"base_url"`
}

// Load reads path. A missing file yields an empty Config, not an error.
func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Classifier builds the domain classifier. No rules means the defaults.
func (c Config) Classifier() (domain.Classifier, error) {
	if len(c.Rules) == 0 && c.Default == "" {
		return domain.DefaultClassifier(), nil
	}
	fallback := domain.KindSecret
	if c.Default != "" {
		k, err := domain.ParseKind(c.Default)
		if err != nil {
			return domain.Classifier{}, fmt.Errorf("default: %w", err)
		}
		fallback = k
	}
	rules := make([]domain.Rule, 0, len(c.Rules))
	for i, r := range c.Rules {
		k, err := domain.ParseKind(r.Type)
		if err != nil {
			return domain.Classifier{}, fmt.Errorf("rules[%d]: %w", i, err)
		}
		rules = append(rules, domain.Rule{Pattern: r.Pattern, Kind: k})
	}
	return domain.NewClassifier(rules, fallback), nil
}

const Template = `# env4ci configuration. Tokens come from GITHUB_TOKEN / GITLAB_TOKEN, never this file.
source: .env

# Unmatched keys default to secret (safe). First matching rule wins.
default: secret
rules:
  - { pattern: "*SECRET*",   type: secret }
  - { pattern: "*PASSWORD*", type: secret }
  - { pattern: "*TOKEN*",    type: secret }
  - { pattern: "*KEY*",      type: secret }
  - { pattern: "APP_*",      type: variable }
  - { pattern: "LOG_*",      type: variable }

# Branch -> environment. "env4ci push" on branch main writes to production.
# branches:
#   main: production
#   develop: staging
#   "release/*": staging
# environments:
#   production: .env.production
#   staging: .env.staging

# Credentials are verified before push (SSH login, registry login).
# Common names (SSH_PRIVATE_KEY + SSH_HOST + SSH_USER, GHCR_TOKEN, DOCKERHUB_TOKEN ...)
# are detected automatically; list others here by variable name.
# checks:
#   ssh:
#     - { key: DEPLOY_KEY, host: DEPLOY_HOST, user: DEPLOY_USER, port: DEPLOY_PORT }
#   registry:
#     - { registry: ghcr.io, username: GHCR_USER, password: GHCR_TOKEN }

# repo/project are read from "git remote origin" when omitted.
targets:
  github:
    # repo: owner/name
    # environment: production   # omit for repository-level
  # gitlab:
  #   project: group/project
  #   environment: production   # omit for "*"
  #   protected: true
`
