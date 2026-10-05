package gitinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := map[string]Remote{
		"https://github.com/bakhod1r/env4ci.git":         {Host: "github.com", Path: "bakhod1r/env4ci", Provider: "github"},
		"https://github.com/bakhod1r/env4ci":             {Host: "github.com", Path: "bakhod1r/env4ci", Provider: "github"},
		"git@github.com:bakhod1r/env4ci.git":             {Host: "github.com", Path: "bakhod1r/env4ci", Provider: "github"},
		"ssh://git@github.com/bakhod1r/env4ci.git":       {Host: "github.com", Path: "bakhod1r/env4ci", Provider: "github"},
		"https://user:tok@github.com/o/r.git":            {Host: "github.com", Path: "o/r", Provider: "github"},
		"git@gitlab.com:group/sub/project.git":           {Host: "gitlab.com", Path: "group/sub/project", Provider: "gitlab"},
		"ssh://git@gitlab.example.com:2222/team/app.git": {Host: "gitlab.example.com", Path: "team/app", Provider: "gitlab"},
		"https://git.corp.io/team/app.git":               {Host: "git.corp.io", Path: "team/app", Provider: ""},
		"https://github.corp.io/team/app.git":            {Host: "github.corp.io", Path: "team/app", Provider: "github"},
	}
	for in, want := range cases {
		got, err := ParseRemote(in)
		if err != nil {
			t.Errorf("ParseRemote(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseRemote(%q) = %+v, want %+v", in, got, want)
		}
	}
	for _, bad := range []string{"", "not a url", "https://github.com/onlyowner", "/local/path/repo"} {
		if _, err := ParseRemote(bad); err == nil {
			t.Errorf("ParseRemote(%q): want error", bad)
		}
	}
}

func TestRemoteBaseURL(t *testing.T) {
	for r, want := range map[Remote]string{
		{Host: "github.com", Provider: "github"}:         "",
		{Host: "github.corp.io", Provider: "github"}:     "https://github.corp.io/api/v3",
		{Host: "gitlab.com", Provider: "gitlab"}:         "",
		{Host: "gitlab.example.com", Provider: "gitlab"}: "https://gitlab.example.com",
	} {
		if got := r.BaseURL(); got != want {
			t.Errorf("%+v.BaseURL() = %q, want %q", r, got, want)
		}
	}
}

func TestReadFromRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, k := range ciBranchVars { // running inside CI must not leak in
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "develop")
	git("remote", "add", "origin", "git@gitlab.com:team/app.git")

	sub := filepath.Join(dir, "a", "b")
	os.MkdirAll(sub, 0o755)

	r, err := ReadRemote(sub)
	if err != nil || r.Path != "team/app" || r.Provider != "gitlab" {
		t.Fatalf("remote = %+v, err = %v", r, err)
	}
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/secret/\n"), 0o644)
	if !IsIgnored(dir, "secret/x.env") || IsIgnored(dir, "env4ci.yaml") {
		t.Fatal("IsIgnored wrong")
	}
	b, err := CurrentBranch(sub)
	if err != nil || b != "develop" {
		t.Fatalf("branch = %q, err = %v", b, err)
	}
}

func TestNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if _, err := ReadRemote(dir); err == nil {
		t.Fatal("want error")
	}
}

func TestBranchFromCIEnv(t *testing.T) {
	for env, want := range map[[2]string]string{
		{"GITHUB_HEAD_REF", "feature/x"}:              "feature/x",
		{"GITHUB_REF_NAME", "main"}:                   "main",
		{"CI_COMMIT_BRANCH", "develop"}:               "develop",
		{"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME", "mr"}: "mr",
	} {
		t.Run(env[0], func(t *testing.T) {
			for _, k := range ciBranchVars {
				t.Setenv(k, "")
			}
			t.Setenv(env[0], env[1])
			if got := branchFromCI(); got != want {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestFilesContentHooks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if fs, err := Files(dir, false); err != nil || fs != nil {
		t.Fatalf("empty repo: %v %v", fs, err)
	}
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("staged"), 0o644)
	git("add", "a.txt")
	git("commit", "-qm", "a")
	git("add", "sub/b.txt")
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("worktree"), 0o644)

	if fs, _ := Files(dir, false); !reflect.DeepEqual(fs, []string{"a.txt", "sub/b.txt"}) {
		t.Fatalf("tracked: %v", fs)
	}
	sub := filepath.Join(dir, "sub")
	if fs, _ := Files(sub, true); !reflect.DeepEqual(fs, []string{"b.txt"}) {
		t.Fatalf("staged from sub: %v", fs)
	}
	if b, err := Content(sub, "b.txt", true); err != nil || string(b) != "staged" {
		t.Fatalf("staged content: %q %v", b, err)
	}
	if b, _ := Content(sub, "b.txt", false); string(b) != "worktree" {
		t.Fatalf("worktree content: %q", b)
	}
	if _, err := Content(dir, "nope", true); err == nil {
		t.Fatal("want error")
	}
	h, err := HooksDir(sub)
	if err != nil || !strings.HasSuffix(filepath.ToSlash(h), ".git/hooks") || !filepath.IsAbs(h) {
		t.Fatalf("hooks = %q, %v", h, err)
	}
	git("config", "core.hooksPath", "/abs/hooks")
	if h, _ := HooksDir(dir); filepath.ToSlash(h) != "/abs/hooks" && !strings.HasSuffix(filepath.ToSlash(h), "/abs/hooks") {
		t.Fatalf("hooksPath = %q", h)
	}
}
