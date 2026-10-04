package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
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
	os.WriteFile(env, []byte("APP_A=1\n"), 0o600)
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

// redactDir hides the temp dir and normalizes the separator after it (Windows).
func redactDir(s, dir string) string {
	s = strings.ReplaceAll(s, dir+string(filepath.Separator), "<dir>/")
	return strings.ReplaceAll(s, dir, "<dir>")
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
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
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
	golden(t, "scan.stdout", redactDir(out.String(), dir))
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

func TestScanByBranchGolden(t *testing.T) {
	dir := copyDir(t, filepath.Join("testdata", "project"))
	var out bytes.Buffer
	args := []string{"scan", "--by", "branch", "--dir", dir, "--write", "-c", filepath.Join(dir, "none.yaml")}
	if err := run(context.Background(), args, nil, &out); err != nil {
		t.Fatal(err)
	}
	golden(t, "branch/scan.stdout", redactDir(out.String(), dir))
	for _, f := range []string{".env.example", ".env.main.example", ".env.develop.example", ".env.tags.example", ".env.default.example", ".env.release.example"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("%s: %v\n%s", f, err, out.String())
		}
		golden(t, "branch/"+strings.TrimPrefix(f, "."), string(b))
	}
}

func TestScanByInvalid(t *testing.T) {
	dir := copyDir(t, filepath.Join("testdata", "project"))
	err := run(context.Background(), []string{"scan", "--by", "stage", "--dir", dir, "-c", filepath.Join(dir, "x.yaml")}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--by") {
		t.Fatalf("err = %v", err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"main": "main", "release/*": "release", `/^release\//`: "release", "(tags)": "tags", "feature/x-1": "feature-x-1", "***": "branch"} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScanFileNameCollision(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte(`
a:
  only: ["release/*"]
  script: [echo $A_TOKEN]
b:
  only: ["/^release\\//"]
  script: [echo $B_TOKEN]
`), 0o644)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"scan", "--by", "branch", "--write", "--dir", dir, "-c", filepath.Join(dir, "x.yaml")}, nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".env.release.example", ".env.release-2.example"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s missing:\n%s", f, out.String())
		}
	}
}

func TestDiffExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"key":"APP_PORT","value":"8080","environment_scope":"*"}]`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("targets:\n  gitlab:\n    project: g/p\n    base_url: "+srv.URL+"\n"), 0o600)
	same := filepath.Join(dir, "same.env")
	os.WriteFile(same, []byte("APP_PORT=8080\n"), 0o600)
	drift := filepath.Join(dir, "drift.env")
	os.WriteFile(drift, []byte("APP_PORT=9090\n"), 0o600)
	t.Setenv("GITLAB_TOKEN", "tok")

	if err := run(context.Background(), []string{"diff", "gitlab", "-c", cfg, "-f", same, "--exit-code"}, nil, io.Discard); err != nil {
		t.Fatalf("no drift: %v", err)
	}
	if err := run(context.Background(), []string{"diff", "gitlab", "-c", cfg, "-f", drift, "--exit-code"}, nil, io.Discard); !errors.Is(err, errDrift) {
		t.Fatalf("drift: %v", err)
	}
	if err := run(context.Background(), []string{"diff", "gitlab", "-c", cfg, "-f", drift}, nil, io.Discard); err != nil {
		t.Fatalf("without flag: %v", err)
	}
}

func TestPushRejectsShortGitLabSecretBeforeWriting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("targets:\n  gitlab:\n    project: g/p\n    base_url: "+srv.URL+"\n"), 0o600)
	env := filepath.Join(dir, ".env")
	os.WriteFile(env, []byte("APP_PORT=1\nJWT_SECRET=short\n"), 0o600)
	t.Setenv("GITLAB_TOKEN", "tok")
	err := run(context.Background(), []string{"push", "gitlab", "-c", cfg, "-f", env, "-y"}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "at least 8") {
		t.Fatalf("err = %v", err)
	}
}

func TestPushBlockedByFailedCredentialCheck(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(priv, "")
	key := strings.ReplaceAll(string(pem.EncodeToMemory(block)), "\n", `\n`)

	var writes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, `[]`)
			return
		}
		writes++
		w.WriteHeader(201)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("targets:\n  gitlab:\n    project: g/p\n    base_url: "+srv.URL+"\n"), 0o600)
	env := filepath.Join(dir, ".env")
	// Port 1 refuses connections: the login cannot succeed.
	os.WriteFile(env, []byte("SSH_PRIVATE_KEY=\""+key+"\"\nSSH_HOST=127.0.0.1:1\nSSH_USER=deployer\nSSH_KNOWN_HOSTS=\"# no hosts here\"\n"), 0o600)
	t.Setenv("GITLAB_TOKEN", "tok")

	var out bytes.Buffer
	err := run(context.Background(), []string{"push", "gitlab", "-c", cfg, "-f", env, "-y"}, nil, &out)
	if !errors.Is(err, errCheckFailed) || writes != 0 {
		t.Fatalf("err=%v writes=%d\n%s", err, writes, out.String())
	}
	if !strings.Contains(out.String(), "✗ ssh deployer@127.0.0.1:1 (SSH_PRIVATE_KEY)") || strings.Contains(out.String(), "PRIVATE KEY-----") {
		t.Fatalf("output:\n%s", out.String())
	}

	// --no-verify skips the check and writes.
	if err := run(context.Background(), []string{"push", "gitlab", "-c", cfg, "-f", env, "-y", "--no-verify"}, nil, io.Discard); err != nil || writes == 0 {
		t.Fatalf("no-verify: err=%v writes=%d", err, writes)
	}
}

func TestVerifyCommandNoCredentials(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	os.WriteFile(env, []byte("APP_PORT=1\n"), 0o600)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"verify", "-f", env, "-c", filepath.Join(dir, "x.yaml")}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No SSH keys") {
		t.Fatalf("%s", out.String())
	}
}

// fakeVault stores one KV v2 secret per path.
func fakeVault(t *testing.T) (*httptest.Server, map[string]map[string]any) {
	store := map[string]map[string]any{}
	versions := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "vtok" {
			w.WriteHeader(403)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/v1/secret/data/")
		switch r.Method {
		case http.MethodGet:
			if versions[p] == 0 {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": store[p], "metadata": map[string]int{"version": versions[p]}}})
		case http.MethodPost:
			var body struct {
				Data map[string]any `json:"data"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			store[p] = body.Data
			versions[p]++
		}
	}))
	t.Cleanup(srv.Close)
	return srv, store
}

func TestVaultAllEnvironments(t *testing.T) {
	srv, store := fakeVault(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	prod := filepath.Join(dir, "prod.env")
	stg := filepath.Join(dir, "stg.env")
	os.WriteFile(prod, []byte("DATABASE_URL=postgres://prod\nAPP_PORT=80\n"), 0o600)
	os.WriteFile(stg, []byte("DATABASE_URL=postgres://stg\n"), 0o600)
	os.WriteFile(cfg, []byte(fmt.Sprintf(`
environments: { production: %q, staging: %q }
targets:
  vault: { address: %q, path: "api/{env}" }
`, prod, stg, srv.URL)), 0o600)
	t.Setenv("VAULT_TOKEN", "vtok")
	t.Setenv("VAULT_ADDR", "")

	var out bytes.Buffer
	if err := run(context.Background(), []string{"push", "--all", "-c", cfg, "-y"}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if store["api/production"]["DATABASE_URL"] != "postgres://prod" || store["api/production"]["APP_PORT"] != "80" ||
		store["api/staging"]["DATABASE_URL"] != "postgres://stg" || len(store["api/staging"]) != 1 {
		t.Fatalf("store = %v\n%s", store, out.String())
	}
	if strings.Contains(out.String(), "postgres://") {
		t.Fatalf("value leaked:\n%s", out.String())
	}

	// Second diff: no drift.
	if err := run(context.Background(), []string{"diff", "--all", "-c", cfg, "--exit-code"}, nil, io.Discard); err != nil {
		t.Fatalf("drift after push: %v", err)
	}
	os.WriteFile(stg, []byte("DATABASE_URL=postgres://changed\n"), 0o600)
	if err := run(context.Background(), []string{"diff", "--all", "-c", cfg, "--exit-code"}, nil, io.Discard); !errors.Is(err, errDrift) {
		t.Fatalf("want drift, got %v", err)
	}
}

func TestAllWithoutEnvironments(t *testing.T) {
	dir := t.TempDir()
	err := run(context.Background(), []string{"diff", "--all", "-c", filepath.Join(dir, "x.yaml")}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "environments:") {
		t.Fatalf("err = %v", err)
	}
}
