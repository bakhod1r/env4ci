package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) (string, func(...string)) {
	t.Helper()
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
	t.Chdir(dir)
	return dir, git
}

func TestLeaks(t *testing.T) {
	dir, git := gitRepo(t)
	os.WriteFile("env4ci.yaml", []byte("environments: { production: prod.env }\n"), 0o644)
	os.WriteFile("prod.env", []byte("API_TOKEN=tok_live_1234567\nLOG_LEVEL=debug-verbose\n"), 0o600)
	os.WriteFile(".gitignore", []byte("prod.env\n"), 0o644)
	os.WriteFile("app.yml", []byte("level: debug-verbose\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "init")

	var out bytes.Buffer
	if err := run(context.Background(), []string{"leaks"}, nil, &out); err != nil {
		t.Fatalf("clean repo: %v\n%s", err, out.String())
	}

	// Committed leak.
	os.WriteFile("app.yml", []byte("level: x\ntoken: tok_live_1234567\n"), 0o644)
	git("commit", "-qam", "leak")
	out.Reset()
	err := run(context.Background(), []string{"leaks"}, nil, &out)
	if !errors.Is(err, errLeak) || !strings.Contains(out.String(), "app.yml:2") || !strings.Contains(out.String(), "API_TOKEN") {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "tok_live") {
		t.Fatalf("value printed:\n%s", out.String())
	}
	if exitCode(err, &bytes.Buffer{}) != 2 {
		t.Fatal("leak must exit 2")
	}

	// --staged looks only at the index: fixed in the worktree but not staged still fails.
	os.WriteFile("app.yml", []byte("level: x\n"), 0o644)
	git("add", "app.yml")
	os.WriteFile("new.txt", []byte("tok_live_1234567"), 0o644) // untracked: ignored
	if err := run(context.Background(), []string{"leaks", "--staged"}, nil, &out); err != nil {
		t.Fatalf("staged clean: %v", err)
	}
	git("add", "new.txt")
	if err := run(context.Background(), []string{"leaks", "--staged"}, nil, &out); !errors.Is(err, errLeak) {
		t.Fatalf("staged leak: %v", err)
	}
	_ = dir
}

func TestLeaksNothingToSearch(t *testing.T) {
	gitRepo(t)
	os.WriteFile("env4ci.yaml", []byte("environments: { production: missing.env }\n"), 0o644)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"leaks"}, nil, &out); err != nil || !strings.Contains(out.String(), "No secret values") {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestLeaksSkipsBinaryAndEnvFiles(t *testing.T) {
	_, git := gitRepo(t)
	os.WriteFile("env4ci.yaml", []byte("source: tracked.env\n"), 0o644)
	os.WriteFile("tracked.env", []byte("API_TOKEN=tok_live_1234567\n"), 0o600) // committed by mistake, still the source
	os.WriteFile("bin.dat", []byte("tok_live_1234567\x00"), 0o644)
	git("add", ".")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"leaks", "--staged"}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestLeaksErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	os.WriteFile("env4ci.yaml", []byte("source: a.env\n"), 0o644)
	os.WriteFile("a.env", []byte("API_TOKEN=tok_live_1234567\n"), 0o600)
	if err := run(context.Background(), []string{"leaks"}, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("not a repo: %v", err)
	}
	os.WriteFile("a.env", []byte("bad line\n"), 0o600)
	if err := run(context.Background(), []string{"leaks"}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("bad env file: want error")
	}
}

func TestHook(t *testing.T) {
	dir, git := gitRepo(t)
	os.WriteFile("env4ci.yaml", []byte("{}\n"), 0o644)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"hook"}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	b, err := os.ReadFile(hook)
	if err != nil || !strings.Contains(string(b), "env4ci leaks --staged") {
		t.Fatalf("hook = %q, %v", b, err)
	}
	out.Reset()
	if err := run(context.Background(), []string{"hook"}, nil, &out); err != nil || !strings.Contains(out.String(), "up to date") {
		t.Fatalf("rerun: %v\n%s", err, out.String())
	}
	os.WriteFile(hook, []byte("#!/bin/sh\nmake lint\n"), 0o755)
	if err := run(context.Background(), []string{"hook"}, nil, &out); err == nil || !strings.Contains(err.Error(), "env4ci leaks --staged") {
		t.Fatalf("foreign hook: %v", err)
	}
	_ = git
}

func TestHookErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	os.WriteFile("env4ci.yaml", []byte("{}\n"), 0o644)
	if err := run(context.Background(), []string{"hook"}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("not a repo: want error")
	}
	_, git := gitRepo(t)
	os.WriteFile("env4ci.yaml", []byte("{}\n"), 0o644)
	git("config", "core.hooksPath", "blocked/hooks")
	os.WriteFile("blocked", nil, 0o644)
	if err := run(context.Background(), []string{"hook"}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("blocked hooks dir: want error")
	}
	git("config", "core.hooksPath", "hooks")
	os.MkdirAll(filepath.Join("hooks", "pre-commit"), 0o755) // a folder where the hook file goes
	if err := run(context.Background(), []string{"hook"}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("unwritable hook: want error")
	}
}
