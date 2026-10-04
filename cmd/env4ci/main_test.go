package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"io/fs"
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

var update = flag.Bool("update", false, "rewrite golden files")

// copyDir copies a fixture tree so tests can write into it.
func copyDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(got), 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./cmd/env4ci -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got\n%s--- want\n%s", name, got, want)
	}
}

func TestScanGolden(t *testing.T) {
	dir := copyDir(t, filepath.Join("testdata", "project"))
	var out bytes.Buffer
	args := []string{"scan", "--dir", dir, "--write", "-f", filepath.Join(dir, "local.env"), "-c", filepath.Join(dir, "none.yaml")}
	if err := run(context.Background(), args, nil, &out); err != nil {
		t.Fatal(err)
	}
	golden(t, "scan.stdout", strings.ReplaceAll(out.String(), dir, "<dir>"))
	for _, f := range []string{".env.example", ".env.production.example", ".env.staging.example"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		golden(t, strings.TrimPrefix(f, "."), string(b))
	}

	// Second run must not overwrite.
	out.Reset()
	if err := run(context.Background(), args, nil, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "skipped") != 3 {
		t.Fatalf("want 3 skips:\n%s", out.String())
	}
}
