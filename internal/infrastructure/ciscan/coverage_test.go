package ciscan

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func node(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func TestGithubTriggerBranches(t *testing.T) {
	cases := map[string][]string{
		"on: push":                                         {domain.BranchAll},
		"on: [push, pull_request]":                         {domain.BranchAll},
		"on: {schedule: [{cron: '0 0 * * *'}]}":            {domain.BranchAll},
		"on: {push: {tags: ['v*']}}":                       {domain.BranchTags},
		"on: {push: {tags: ['v*'], branches-ignore: [x]}}": {domain.BranchAll},
		"on: {push: {paths: ['src/**']}}":                  {domain.BranchAll},
		"on: {push: {branches: main}}":                     {"main"},
		"on: {}":                                           {domain.BranchAll},
	}
	for src, want := range cases {
		got := githubTriggerBranches(mapGet(node(t, src), "on"))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v want %v", src, got, want)
		}
	}
	if got := githubTriggerBranches(nil); !reflect.DeepEqual(got, []string{domain.BranchAll}) {
		t.Errorf("nil: %v", got)
	}
}

func TestGithubIfBranches(t *testing.T) {
	got := githubIfBranches(`github.ref == 'refs/heads/main' || github.ref_name == "develop"`)
	if !reflect.DeepEqual(got, []string{"main", "develop"}) {
		t.Fatalf("%v", got)
	}
}

func TestGitlabBranchesRules(t *testing.T) {
	cases := map[string][]string{
		"rules: [{if: '$CI_COMMIT_TAG'}]":                                 {domain.BranchTags},
		"rules: [{if: '$CI_PIPELINE_SOURCE == \"merge_request_event\"'}]": {domain.BranchAll},
		"rules: [{when: manual}]":                                         {domain.BranchAll},
		"rules: [{if: '$CI_COMMIT_BRANCH == \"x\"', when: never}]":        {domain.BranchAll},
		"rules: [{if: \"$CI_COMMIT_REF_NAME == 'rc'\"}]":                  {"rc"},
		"only: {variables: [$X]}":                                         {domain.BranchAll},
		"only: [api]":                                                     {domain.BranchAll},
		"only: [main, tags]":                                              {"main", domain.BranchTags},
		"rules: {}":                                                       {domain.BranchAll},
	}
	for src, want := range cases {
		if got := gitlabBranches(node(t, src)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v want %v", src, got, want)
		}
	}
}

func TestScalarsAndEnvironmentOf(t *testing.T) {
	if scalars(nil) != nil {
		t.Fatal("nil")
	}
	if got := scalars(node(t, "x")); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("%v", got)
	}
	if got := scalars(node(t, "[a, {b: c}, d]")); !reflect.DeepEqual(got, []string{"a", "d"}) {
		t.Fatalf("%v", got)
	}
	if environmentOf(node(t, "environment: {url: x}")) != "" || environmentOf(node(t, "environment: [a]")) != "" {
		t.Fatal("environment without name")
	}
}

func TestLoadEdgeCases(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	os.MkdirAll(wf, 0o755)

	// Empty and non-mapping workflow files are ignored, not errors.
	os.WriteFile(filepath.Join(wf, "empty.yml"), nil, 0o644)
	os.WriteFile(filepath.Join(wf, "list.yml"), []byte("- a\n- b\n"), 0o644)
	if refs, err := Scan(dir, domain.DefaultClassifier()); err != nil || len(refs) != 0 {
		t.Fatalf("refs=%v err=%v", refs, err)
	}

	// A directory named like a workflow cannot be read.
	os.Mkdir(filepath.Join(wf, "dir.yml"), 0o755)
	if _, err := Scan(dir, domain.DefaultClassifier()); err == nil {
		t.Fatal("want read error")
	}
}

func TestGitLabStatAndIncludeErrors(t *testing.T) {
	// root is a file: stat of root/.gitlab-ci.yml fails with ENOTDIR
	// (Windows reports "not found" instead, which is not an error).
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	if _, err := scanGitLabDir(f, domain.DefaultClassifier()); err == nil && runtime.GOOS != "windows" {
		t.Fatal("want stat error")
	}

	// An include that is a directory cannot be loaded.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte("include: inc.yml\n"), 0o644)
	os.Mkdir(filepath.Join(dir, "inc.yml"), 0o755)
	if _, err := Scan(dir, domain.DefaultClassifier()); err == nil || !strings.Contains(err.Error(), "inc.yml") {
		t.Fatalf("err = %v", err)
	}
}
