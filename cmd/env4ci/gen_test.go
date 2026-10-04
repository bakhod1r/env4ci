package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
)

const genCfg = `
auth_file: .env4ci/env4ci.env
environments: { production: .env4ci/production.env, staging: .env4ci/staging.env }
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
	for _, f := range []string{"env4ci.env", "production.env", "staging.env"} {
		b, err := os.ReadFile(filepath.Join(dir, ".env4ci", f))
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "gen/"+f, string(b))
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Count(string(gi), "/.env4ci/\n") != 1 {
		t.Errorf("folder not gitignored exactly once:\n%s", gi)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env4ci", ".gitignore")); err == nil {
		t.Error("no .gitignore inside .env4ci/ expected")
	}
	if st, err := os.Stat(filepath.Join(dir, ".env4ci")); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o700) {
		t.Errorf("folder: %v %v", st, err)
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
	prod := filepath.Join(dir, ".env4ci", "production.env")
	os.MkdirAll(filepath.Dir(prod), 0o700)
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

func TestProtect(t *testing.T) {
	base := t.TempDir()
	var out bytes.Buffer
	ui := palette{}
	for _, f := range []string{"top.env", "secrets/a/b.env", "secrets/c.env", "../outside.env"} {
		if err := protect(base, f, &out, ui); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	gi, _ := os.ReadFile(filepath.Join(base, ".gitignore"))
	if string(gi) != "top.env\n/secrets/\n" {
		t.Fatalf(".gitignore = %q", gi)
	}
	if _, err := os.Stat(filepath.Join(base, "secrets", "a")); err != nil {
		t.Fatal("nested folder not created")
	}
}

func TestConfigNeverGitignored(t *testing.T) {
	// The normal layout keeps env4ci.yaml out of .gitignore.
	dir, cfgPath := genProject(t)
	if err := run(context.Background(), []string{"gen", "-c", cfgPath, "--dir", dir}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if strings.Contains(string(gi), "env4ci.yaml") {
		t.Fatalf(".gitignore lists the config:\n%s", gi)
	}

	for name, tc := range map[string]struct{ cfgRel, yaml string }{
		"environment file is the config": {"env4ci.yaml", "environments: { production: env4ci.yaml }\ntargets: { github: { repo: a/b } }\n"},
		"auth file is the config":        {"env4ci.yaml", "auth_file: env4ci.yaml\ntargets: { github: { repo: a/b } }\n"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, tc.cfgRel)
			os.MkdirAll(filepath.Dir(cfgPath), 0o700)
			os.WriteFile(cfgPath, []byte(tc.yaml), 0o644)
			err := run(context.Background(), []string{"gen", "-c", cfgPath, "--dir", dir}, nil, io.Discard)
			if err == nil {
				t.Fatal("want error")
			}
			if gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); len(gi) != 0 {
				t.Fatalf(".gitignore written despite error:\n%s", gi)
			}
			if b, _ := os.ReadFile(cfgPath); string(b) != tc.yaml {
				t.Fatal("config modified")
			}
		})
	}
}

func TestKeepConfigTracked(t *testing.T) {
	d := t.TempDir()
	cfg := filepath.Join(d, "env4ci.yaml")
	for file, ok := range map[string]bool{
		filepath.Join(d, ".env4ci", "p.env"): true,
		filepath.Join(d, "p.env"):            true,
		cfg:                                  false,
		filepath.Join(d, "..", "x.env"):      true,
	} {
		if err := keepConfigTracked(cfg, file); (err == nil) != ok {
			t.Errorf("%s: err=%v", file, err)
		}
	}
}

func TestGenRespectsCommentedKeys(t *testing.T) {
	dir, cfgPath := genProject(t)
	prod := filepath.Join(dir, ".env4ci", "production.env")
	os.MkdirAll(filepath.Dir(prod), 0o700)
	os.WriteFile(prod, []byte("# JWT_SECRET=disabled\n"), 0o600)
	if err := run(context.Background(), []string{"gen", "-c", cfgPath, "--dir", dir}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(prod)
	if strings.Contains(string(b), "\nJWT_SECRET=") {
		t.Fatalf("commented key re-added:\n%s", b)
	}
}

func TestEnvFileKeysGroupsSSHAndRegistry(t *testing.T) {
	keys := envFileKeys("production", []domain.Reference{
		{Key: "DATABASE_URL", Environment: "production"},
		{Key: "SSH_HOST", Environment: "production"},
		{Key: "CODECOV_TOKEN"},
		{Key: "GHCR_TOKEN"},
		{Key: "SSH_PRIVATE_KEY", Environment: "production"},
		{Key: "SSH_USER", Environment: "production"},
	})
	var got []string
	for _, k := range keys {
		got = append(got, k.Group+":"+k.Key)
	}
	want := "ssh:SSH_HOST ssh:SSH_PRIVATE_KEY ssh:SSH_USER ssh:SSH_KNOWN_HOSTS registry:GHCR_TOKEN environment production:DATABASE_URL shared:CODECOV_TOKEN"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}
