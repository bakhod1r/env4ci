package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
)

const genCfg = `
environments: { production: .env.production, staging: .env.staging }
targets:
  github: { repo: acme/api }
  vault: { address: "https://vault.acme.io", path: "api/{env}" }
`

func genProject(t *testing.T) (dir, cfgPath string) {
	t.Helper()
	dir = copyDir(t, filepath.Join("testdata", "project"))
	cfgPath = filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfgPath, []byte(genCfg), 0o600)
	return dir, cfgPath
}

func TestGenGolden(t *testing.T) {
	dir, cfgPath := genProject(t)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"gen", "-c", cfgPath, "--dir", dir}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	golden(t, "gen/stdout", redactDir(out.String(), dir))
	for _, f := range []string{".env.env4ci", ".env.production", ".env.staging"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "gen/"+strings.TrimPrefix(f, "."), string(b))
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, f := range []string{".env.env4ci", ".env.production", ".env.staging"} {
		if !strings.Contains(string(gi), f+"\n") {
			t.Errorf("%s not gitignored:\n%s", f, gi)
		}
	}

	// Second run changes nothing.
	out.Reset()
	if err := run(context.Background(), []string{"gen", "-c", cfgPath, "--dir", dir}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "is complete") != 3 {
		t.Fatalf("second run:\n%s", out.String())
	}
}

func TestGenKeepsValuesAndAppendsMissing(t *testing.T) {
	dir, cfgPath := genProject(t)
	prod := filepath.Join(dir, ".env.production")
	os.WriteFile(prod, []byte("DATABASE_URL=postgres://keep\nCUSTOM=1"), 0o600) // no trailing newline
	var out bytes.Buffer
	if err := run(context.Background(), []string{"gen", "-c", cfgPath, "--dir", dir}, nil, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(prod)
	s := string(b)
	if !strings.HasPrefix(s, "DATABASE_URL=postgres://keep\nCUSTOM=1\n\n# --- added by env4ci gen ---\n") {
		t.Fatalf("existing content changed:\n%s", s)
	}
	if strings.Count(s, "DATABASE_URL=") != 1 || !strings.Contains(s, "\nJWT_SECRET=\n") {
		t.Fatalf("append wrong:\n%s", s)
	}
	if !strings.Contains(out.String(), "added") {
		t.Fatalf("%s", out.String())
	}
}

func TestGenNeedsTargets(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfgPath, []byte("source: .env\n"), 0o600)
	if err := run(context.Background(), []string{"gen", "-c", cfgPath}, nil, io.Discard); err == nil || !strings.Contains(err.Error(), "no targets") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadAuthFile(t *testing.T) {
	dir := t.TempDir()
	auth := filepath.Join(dir, "auth.env")
	cfg := config.Config{AuthFile: auth}
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "from-env")

	os.WriteFile(auth, []byte("VAULT_TOKEN=from-file\nGITLAB_TOKEN=from-file\nGITHUB_TOKEN=\n"), 0o600)
	if err := loadAuthFile(cfg, io.Discard); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("VAULT_TOKEN") != "from-file" || os.Getenv("GITLAB_TOKEN") != "from-env" {
		t.Fatalf("VAULT_TOKEN=%q GITLAB_TOKEN=%q", os.Getenv("VAULT_TOKEN"), os.Getenv("GITLAB_TOKEN"))
	}

	os.WriteFile(auth, []byte("DATABASE_URL=x\n"), 0o600)
	if err := loadAuthFile(cfg, io.Discard); err == nil || !strings.Contains(err.Error(), "not an env4ci setting") {
		t.Fatalf("err = %v", err)
	}

	if err := loadAuthFile(config.Config{AuthFile: filepath.Join(dir, "missing")}, io.Discard); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}

func TestPushRefusesAuthFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "env4ci.yaml")
	auth := filepath.Join(dir, ".env.env4ci")
	os.WriteFile(auth, []byte("VAULT_TOKEN=t\n"), 0o600)
	os.WriteFile(cfgPath, []byte("auth_file: "+auth+"\ntargets:\n  vault: { address: http://127.0.0.1:1, path: p }\n"), 0o600)
	err := run(context.Background(), []string{"push", "-c", cfgPath, "-f", auth}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "refusing to sync") {
		t.Fatalf("err = %v", err)
	}
}
