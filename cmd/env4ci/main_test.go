package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateNeverPrintsValues(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	os.WriteFile(env, []byte("JWT_SECRET=topsecret\nAPP_PORT=8080\n"), 0o600)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"validate", "-f", env, "-c", filepath.Join(dir, "none.yaml")}, nil, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "topsecret") || strings.Contains(s, "8080") {
		t.Fatalf("leaked value:\n%s", s)
	}
	if !strings.Contains(s, "JWT_SECRET") || !strings.Contains(s, "APP_PORT") {
		t.Fatalf("missing keys:\n%s", s)
	}
}

func TestPushGitLabEndToEnd(t *testing.T) {
	var writes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			io.WriteString(w, `[{"key":"APP_PORT","value":"8080","environment_scope":"*"}]`)
		default:
			writes = append(writes, r.Method+" "+r.URL.Path)
			w.WriteHeader(404) // PUT misses, POST follows
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("targets:\n  gitlab:\n    project: g/p\n    base_url: "+srv.URL+"\n"), 0o600)
	env := filepath.Join(dir, ".env")
	os.WriteFile(env, []byte("APP_PORT=8080\nJWT_SECRET=topsecret1\n"), 0o600)
	t.Setenv("GITLAB_TOKEN", "tok")

	var out bytes.Buffer
	err := run(context.Background(), []string{"push", "gitlab", "-c", cfg, "-f", env}, strings.NewReader("y\n"), &out)
	// POST returns 404 from this fake too, so the write fails; we only assert flow and no leaks.
	if err == nil {
		t.Fatal("expected fake server write error")
	}
	if strings.Contains(out.String(), "topsecret1") {
		t.Fatalf("leaked value:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "+ JWT_SECRET") || len(writes) != 2 {
		t.Fatalf("out:\n%s\nwrites=%v", out.String(), writes)
	}
}

func TestPushAbortWithoutConfirm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write %s", r.Method)
		}
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("targets:\n  gitlab:\n    project: g/p\n    base_url: "+srv.URL+"\n"), 0o600)
	env := filepath.Join(dir, ".env")
	os.WriteFile(env, []byte("A=1\n"), 0o600)
	t.Setenv("GITLAB_TOKEN", "tok")
	err := run(context.Background(), []string{"push", "gitlab", "-c", cfg, "-f", env}, strings.NewReader("n\n"), io.Discard)
	if err == nil || err.Error() != "aborted" {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureGitignored(t *testing.T) {
	gi := filepath.Join(t.TempDir(), ".gitignore")
	os.WriteFile(gi, []byte("bin"), 0o644)
	if added, _ := ensureGitignored(gi, ".env.prod"); !added {
		t.Fatal("want added")
	}
	if added, _ := ensureGitignored(gi, ".env.prod"); added {
		t.Fatal("want idempotent")
	}
	b, _ := os.ReadFile(gi)
	if string(b) != "bin\n.env.prod\n" {
		t.Fatalf("%q", b)
	}
}
