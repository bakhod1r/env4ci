// Package ciscan finds variables that CI pipeline files expect to be defined
// in CI settings: ${{ secrets.X }} / ${{ vars.X }} in GitHub workflows and
// $X / ${X} in .gitlab-ci.yml (minus predefined and file-defined variables).
package ciscan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bakhod1r/env4ci/internal/domain"
)

// Scan looks for .github/workflows/*.yml|yaml and .gitlab-ci.yml under root.
func Scan(root string, cl domain.Classifier) ([]domain.Reference, error) {
	var refs []domain.Reference
	for _, pat := range []string{"*.yml", "*.yaml"} {
		files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", pat))
		for _, f := range files {
			r, err := scanFile(root, f, scanGitHub)
			if err != nil {
				return nil, err
			}
			refs = append(refs, r...)
		}
	}
	gl := filepath.Join(root, ".gitlab-ci.yml")
	if _, err := os.Stat(gl); err == nil {
		r, err := scanFile(root, gl, func(doc *yaml.Node, src string) []domain.Reference {
			return scanGitLab(doc, src, cl)
		})
		if err != nil {
			return nil, err
		}
		refs = append(refs, r...)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return domain.MergeReferences(refs), nil
}

func scanFile(root, path string, fn func(*yaml.Node, string) []domain.Reference) ([]domain.Reference, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	return fn(doc.Content[0], filepath.ToSlash(rel)), nil
}

var (
	ghExpr = regexp.MustCompile(`\$\{\{(.*?)\}\}`)
	ghRef  = regexp.MustCompile(`\b(secrets|vars)\.([A-Za-z_][A-Za-z0-9_]*)`)
)

func scanGitHub(root *yaml.Node, src string) []domain.Reference {
	var refs []domain.Reference
	emit := func(n *yaml.Node, env string) {
		walkScalars(n, func(s string) {
			for _, expr := range ghExpr.FindAllStringSubmatch(s, -1) {
				for _, m := range ghRef.FindAllStringSubmatch(expr[1], -1) {
					if m[2] == "GITHUB_TOKEN" {
						continue
					}
					kind := domain.KindVariable
					if m[1] == "secrets" {
						kind = domain.KindSecret
					}
					refs = append(refs, domain.Reference{Key: m[2], Kind: kind, Provider: "github", Environment: env, Sources: []string{src}})
				}
			}
		})
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value != "jobs" || v.Kind != yaml.MappingNode {
			emit(v, "")
			continue
		}
		for j := 0; j+1 < len(v.Content); j += 2 {
			job := v.Content[j+1]
			emit(job, environmentOf(job))
		}
	}
	return refs
}

// environmentOf reads `environment: name` or `environment: {name: x}`.
func environmentOf(job *yaml.Node) string {
	env := mapGet(job, "environment")
	if env == nil {
		return ""
	}
	if env.Kind == yaml.ScalarNode {
		return env.Value
	}
	if n := mapGet(env, "name"); n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

var (
	glRef = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)
	// Variables a runner shell or GitLab always provides.
	glBuiltin = map[string]bool{"HOME": true, "PATH": true, "PWD": true, "OLDPWD": true, "USER": true, "SHELL": true, "HOSTNAME": true, "TERM": true, "RANDOM": true}
	glKeyword = map[string]bool{"stages": true, "variables": true, "default": true, "include": true, "workflow": true, "image": true, "services": true, "before_script": true, "after_script": true, "cache": true}
)

func scanGitLab(root *yaml.Node, src string, cl domain.Classifier) []domain.Reference {
	defined := map[string]bool{}
	collect := func(n *yaml.Node) {
		if vars := mapGet(n, "variables"); vars != nil && vars.Kind == yaml.MappingNode {
			for i := 0; i < len(vars.Content); i += 2 {
				defined[vars.Content[i].Value] = true
			}
		}
	}
	collect(root)
	for i := 1; i < len(root.Content); i += 2 {
		if root.Content[i].Kind == yaml.MappingNode {
			collect(root.Content[i])
		}
	}

	var refs []domain.Reference
	emit := func(n *yaml.Node, env string) {
		walkScalars(n, func(s string) {
			s = strings.ReplaceAll(s, "$$", "") // escaped dollar
			for _, m := range glRef.FindAllStringSubmatch(s, -1) {
				key := m[1]
				if defined[key] || glBuiltin[key] || strings.HasPrefix(key, "CI_") || strings.HasPrefix(key, "GITLAB_") {
					continue
				}
				refs = append(refs, domain.Reference{Key: key, Kind: cl.Classify(key), Provider: "gitlab", Environment: env, Sources: []string{src}})
			}
		})
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i].Value, root.Content[i+1]
		if glKeyword[k] || strings.HasPrefix(k, ".") || v.Kind != yaml.MappingNode {
			emit(v, "")
			continue
		}
		emit(v, environmentOf(v))
	}
	return refs
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// walkScalars visits scalar values only (mapping keys are skipped).
func walkScalars(n *yaml.Node, fn func(string)) {
	switch n.Kind {
	case yaml.ScalarNode:
		fn(n.Value)
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			walkScalars(n.Content[i], fn)
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			walkScalars(c, fn)
		}
	case yaml.AliasNode:
		if n.Alias != nil {
			walkScalars(n.Alias, fn)
		}
	}
}
