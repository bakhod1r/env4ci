// Package ciscan finds variables that CI pipeline files expect to be defined
// in CI settings: ${{ secrets.X }} / ${{ vars.X }} in GitHub workflows and
// $X / ${X} in .gitlab-ci.yml plus its `include: local` files (minus
// predefined and file-defined variables). Each reference records the stage
// it is used in: the GitLab stage, or the GitHub job id.
package ciscan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bakhod1r/env4ci/internal/domain"
)

// StageAll marks usage outside any one stage (workflow-level env, GitLab default:).
const StageAll = "all"

// Scan looks for .github/workflows/*.yml|yaml and .gitlab-ci.yml under root.
func Scan(root string, cl domain.Classifier) ([]domain.Reference, error) {
	gh, err := scanGitHubDir(root)
	if err != nil {
		return nil, err
	}
	gl, err := scanGitLabDir(root, cl)
	if err != nil {
		return nil, err
	}
	return domain.MergeReferences(append(gh, gl...)), nil
}

type file struct {
	rel  string
	root *yaml.Node // top-level mapping
}

func load(root, path string) (*file, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return &file{rel: rel, root: &yaml.Node{Kind: yaml.MappingNode}}, nil
	}
	return &file{rel: rel, root: doc.Content[0]}, nil
}

// --- GitHub ---

var (
	ghExpr = regexp.MustCompile(`\$\{\{(.*?)\}\}`)
	ghRef  = regexp.MustCompile(`\b(secrets|vars)\.([A-Za-z_][A-Za-z0-9_]*)`)
)

func scanGitHubDir(root string) ([]domain.Reference, error) {
	var paths []string
	for _, pat := range []string{"*.yml", "*.yaml"} {
		m, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", pat))
		paths = append(paths, m...)
	}
	sort.Strings(paths)
	var refs []domain.Reference
	for _, p := range paths {
		f, err := load(root, p)
		if err != nil {
			return nil, err
		}
		refs = append(refs, scanGitHub(f)...)
	}
	return refs, nil
}

func scanGitHub(f *file) []domain.Reference {
	var refs []domain.Reference
	emit := func(n *yaml.Node, env, stage string) {
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
					refs = append(refs, domain.Reference{Key: m[2], Kind: kind, Provider: "github",
						Environment: env, Stages: []string{stage}, Sources: []string{f.rel}})
				}
			}
		})
	}
	eachPair(f.root, func(k string, v *yaml.Node) {
		if k != "jobs" || v.Kind != yaml.MappingNode {
			emit(v, "", StageAll)
			return
		}
		eachPair(v, func(jobID string, job *yaml.Node) {
			emit(job, environmentOf(job), jobID)
		})
	})
	return refs
}

// --- GitLab ---

var (
	glRef = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)
	// Variables a runner shell always provides.
	glBuiltin = map[string]bool{"HOME": true, "PATH": true, "PWD": true, "OLDPWD": true, "USER": true, "SHELL": true, "HOSTNAME": true, "TERM": true, "RANDOM": true}
	// Top-level keys that are not jobs.
	glKeyword = map[string]bool{"stages": true, "variables": true, "default": true, "include": true, "workflow": true, "image": true, "services": true, "before_script": true, "after_script": true, "cache": true}
)

// scanGitLabDir reads .gitlab-ci.yml and follows `include: local` (globs
// allowed) recursively. Remote/template/project includes are not fetched.
func scanGitLabDir(root string, cl domain.Classifier) ([]domain.Reference, error) {
	entry := filepath.Join(root, ".gitlab-ci.yml")
	if _, err := os.Stat(entry); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var files []*file
	seen := map[string]bool{}
	queue := []string{entry}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		f, err := load(root, p)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
		for _, inc := range localIncludes(f.root) {
			matches, _ := filepath.Glob(filepath.Join(root, strings.TrimPrefix(inc, "/")))
			if len(matches) == 0 {
				return nil, fmt.Errorf("%s: include %q matches no file", f.rel, inc)
			}
			sort.Strings(matches)
			queue = append(queue, matches...)
		}
	}

	// Variables defined anywhere in the pipeline are not CI settings.
	defined := map[string]bool{}
	for _, f := range files {
		collectDefined(f.root, defined)
		eachPair(f.root, func(_ string, v *yaml.Node) { collectDefined(v, defined) })
	}

	var refs []domain.Reference
	for _, f := range files {
		emit := func(n *yaml.Node, env string, stages []string) {
			walkScalars(n, func(s string) {
				s = strings.ReplaceAll(s, "$$", "") // $$ is an escaped dollar
				for _, m := range glRef.FindAllStringSubmatch(s, -1) {
					key := m[1]
					if defined[key] || glBuiltin[key] || strings.HasPrefix(key, "CI_") || strings.HasPrefix(key, "GITLAB_") {
						continue
					}
					refs = append(refs, domain.Reference{Key: key, Kind: cl.Classify(key), Provider: "gitlab",
						Environment: env, Stages: stages, Sources: []string{f.rel}})
				}
			})
		}
		eachPair(f.root, func(k string, v *yaml.Node) {
			switch {
			case k == "include" || k == "variables" || k == "stages" || k == "workflow":
				// definitions only
			case glKeyword[k]:
				emit(v, "", []string{StageAll})
			case strings.HasPrefix(k, "."): // hidden template; counted where it is merged in
				emit(v, "", nil)
			case v.Kind == yaml.MappingNode:
				emit(v, environmentOf(v), []string{stageOf(v)})
			}
		})
	}
	return refs, nil
}

// localIncludes returns `include:` entries that point at files in the repo.
func localIncludes(root *yaml.Node) []string {
	inc := mapGet(root, "include")
	if inc == nil {
		return nil
	}
	items := []*yaml.Node{inc}
	if inc.Kind == yaml.SequenceNode {
		items = inc.Content
	}
	var out []string
	for _, it := range items {
		switch it.Kind {
		case yaml.ScalarNode: // short form: local path, or URL
			if !strings.Contains(it.Value, "://") {
				out = append(out, it.Value)
			}
		case yaml.MappingNode:
			if l := mapGet(it, "local"); l != nil && l.Kind == yaml.ScalarNode {
				out = append(out, l.Value)
			}
		}
	}
	return out
}

func collectDefined(n *yaml.Node, into map[string]bool) {
	eachPair(mapGet(n, "variables"), func(k string, _ *yaml.Node) { into[k] = true })
}

// stageOf returns a job's stage; GitLab defaults to "test".
func stageOf(job *yaml.Node) string {
	if s := mapGet(job, "stage"); s != nil && s.Kind == yaml.ScalarNode {
		return s.Value
	}
	return "test"
}

// --- shared helpers ---

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

func eachPair(n *yaml.Node, fn func(string, *yaml.Node)) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		fn(n.Content[i].Value, n.Content[i+1])
	}
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	var out *yaml.Node
	eachPair(n, func(k string, v *yaml.Node) {
		if k == key && out == nil {
			out = v
		}
	})
	if out != nil {
		return out
	}
	// Look through YAML merge keys (<<: *anchor).
	eachPair(n, func(k string, v *yaml.Node) {
		if k == "<<" && out == nil {
			if v.Kind == yaml.AliasNode {
				v = v.Alias
			}
			out = mapGet(v, key)
		}
	})
	return out
}

// walkScalars visits scalar values only (mapping keys are skipped) and
// follows aliases, so merged templates are scanned in each job.
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
