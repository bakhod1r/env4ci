package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFindsKeysCIUsesButRemoteLacks(t *testing.T) {
	srv, store := fakeVault(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte(`
lint:
  script: [echo $SONAR_TOKEN]
deploy:
  environment: production
  script: [echo $DATABASE_URL $API_KEY]
`), 0o600)
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte(fmt.Sprintf(`
environments: { production: prod.env }
targets:
  vault: { address: %q, path: "api/{env}" }
`, srv.URL)), 0o600)
	t.Setenv("VAULT_TOKEN", "vtok")
	t.Setenv("VAULT_ADDR", "")
	run(context.Background(), []string{"push", "-c", cfg, "--shared", "-y", "-f", writeEnv(t, dir, "SONAR_TOKEN=s\n")}, nil, &bytes.Buffer{})
	run(context.Background(), []string{"push", "-c", cfg, "-e", "production", "-y", "-f", writeEnv(t, dir, "DATABASE_URL=d\n")}, nil, &bytes.Buffer{})
	if store["api/shared"]["SONAR_TOKEN"] != "s" || store["api/production"]["DATABASE_URL"] != "d" {
		t.Fatalf("setup: %v", store)
	}

	var out bytes.Buffer
	err := run(context.Background(), []string{"check", "-c", cfg, "-e", "production", "--dir", dir}, nil, &out)
	if !errors.Is(err, errDrift) {
		t.Fatalf("want drift, got %v\n%s", err, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "API_KEY") || strings.Contains(s, "DATABASE_URL") || strings.Contains(s, "SONAR_TOKEN") {
		t.Fatalf("output:\n%s", s)
	}

	store["api/production"]["API_KEY"] = "k"
	out.Reset()
	if err := run(context.Background(), []string{"check", "--all", "-c", cfg, "--dir", dir}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "every key CI uses") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func writeEnv(t *testing.T, dir, body string) string {
	t.Helper()
	f, err := os.CreateTemp(dir, "*.env")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(body)
	f.Close()
	return f.Name()
}

func TestCheckErrors(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "vtok")
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("GITHUB_TOKEN", "x")
	write := func(body string) (string, string) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "env4ci.yaml")
		os.WriteFile(cfg, []byte(body), 0o600)
		return dir, cfg
	}
	expect := func(want string, args ...string) {
		t.Helper()
		err := run(context.Background(), args, nil, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%v: err = %v, want %q", args, err, want)
		}
	}
	down := "http://127.0.0.1:1"

	dir, cfg := write("rules: [{pattern: x, type: bad}]\ntargets: { vault: { address: x, path: p } }\n")
	expect("rules[0]", "check", "-c", cfg, "--dir", dir)

	dir, cfg = write("targets: { vault: { address: x, path: p } }\n")
	os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755)
	os.WriteFile(filepath.Join(dir, ".github", "workflows", "x.yml"), []byte("jobs: [x"), 0o644)
	expect("x.yml", "check", "-c", cfg, "--dir", dir)

	// GitHub target: only GitHub refs count; list fails.
	dir, cfg = write(fmt.Sprintf("targets: { github: { repo: o/r, base_url: %q } }\n", down))
	os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte("a:\n  script: [echo $X]\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755)
	os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte("jobs:\n  a:\n    steps:\n      - run: \"echo ${{ secrets.Y }}\"\n"), 0o644)
	expect("github", "check", "-c", cfg, "--dir", dir)

	// Shared level list fails while the environment level works.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/shared") {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	dir, cfg = write(fmt.Sprintf("targets: { vault: { address: %q, path: \"api/{env}\" } }\n", srv.URL))
	expect("shared", "check", "-c", cfg, "-e", "production", "--dir", dir)
}
