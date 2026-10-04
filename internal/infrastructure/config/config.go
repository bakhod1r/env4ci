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
}

type Rule struct {
	Pattern string `yaml:"pattern"`
	Type    string `yaml:"type"`
}

type Targets struct {
	GitHub *GitHub `yaml:"github"`
	GitLab *GitLab `yaml:"gitlab"`
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

targets:
  github:
    repo: owner/name
    # environment: production   # omit for repository-level
  # gitlab:
  #   project: group/project
  #   environment: production   # omit for "*"
  #   protected: true
`
