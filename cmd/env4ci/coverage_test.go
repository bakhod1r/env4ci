package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/dotenv"
)

func unixOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on Windows")
	}
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(context.Background(), args, strings.NewReader(""), &out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func runErr(t *testing.T, want string, args ...string) {
	t.Helper()
	var out bytes.Buffer
	err := run(context.Background(), args, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%v: err = %v, want %q\n%s", args, err, want, out.String())
	}
}

// vaultProject writes env4ci.yaml + env files pointing at a fake Vault.
func vaultProject(t *testing.T, envs map[string]string) (dir, cfg string, store map[string]map[string]any) {
	t.Helper()
	srv, store := fakeVault(t)
	dir = t.TempDir()
	cfg = filepath.Join(dir, "env4ci.yaml")
	var y strings.Builder
	y.WriteString("auth_file: " + filepath.Join(dir, "auth.env") + "\nenvironments:\n")
	for env, content := range envs {
		f := filepath.Join(dir, env+".env")
		os.WriteFile(f, []byte(content), 0o600)
		y.WriteString("  " + env + ": " + f + "\n")
	}
	y.WriteString("targets:\n  vault: { address: \"" + srv.URL + "\", path: \"app/{env}\" }\n")
	os.WriteFile(cfg, []byte(y.String()), 0o600)
	t.Setenv("VAULT_TOKEN", "vtok")
	t.Setenv("VAULT_ADDR", "")
	return dir, cfg, store
}

// --- main / exit codes -------------------------------------------------

func TestMainFunction(t *testing.T) {
	oldExit, oldIn, oldOut, oldErr, oldArgs := exit, stdin, stdout, stderr, os.Args
	defer func() { exit, stdin, stdout, stderr, os.Args = oldExit, oldIn, oldOut, oldErr, oldArgs }()
	var code int
	var out, errOut bytes.Buffer
	exit = func(c int) { code = c }
	stdin, stdout, stderr = strings.NewReader(""), &out, &errOut

	os.Args = []string{"env4ci", "version"}
	main()
	if code != 0 || !strings.HasPrefix(out.String(), "env4ci ") {
		t.Fatalf("version: code=%d out=%q", code, out.String())
	}
	os.Args = []string{"env4ci", "nope"}
	main()
	if code != 1 || !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("error: code=%d err=%q", code, errOut.String())
	}
	if exitCode(errDrift, io.Discard) != 2 {
		t.Fatal("drift must exit 2")
	}
}

// --- run: dispatch -----------------------------------------------------

func TestRunDispatchErrors(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	if s := mustRun(t); !strings.Contains(s, "Usage:") {
		t.Fatal("no-arg help")
	}
	if s := mustRun(t, "-h"); !strings.Contains(s, "Usage:") {
		t.Fatal("-h help")
	}
	runErr(t, "flag provided but not defined", "push", "--nope")
	runErr(t, "unknown command", "frobnicate", "-c", cfg)
	runErr(t, "at most one provider", "diff", "github", "gitlab", "-c", cfg)
	runErr(t, dir, "validate", "-c", dir) // config path is a directory (message differs per OS)

	// auth file with a CI variable is refused at start-up.
	os.WriteFile(filepath.Join(dir, "auth.env"), []byte("DATABASE_URL=x\n"), 0o600)
	os.WriteFile(cfg, []byte("auth_file: "+filepath.Join(dir, "auth.env")+"\n"), 0o600)
	runErr(t, "not an env4ci setting", "validate", "-c", cfg)
}

func TestRunInitTemplateAndExists(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	if s := mustRun(t, "init", "-c", cfg); !strings.Contains(s, "wrote") {
		t.Fatal(s)
	}
	runErr(t, "exists", "init", "-c", cfg)
}

func TestRunInitWizardInteractivePath(t *testing.T) {
	dir := t.TempDir()
	o := opts{config: filepath.Join(dir, "env4ci.yaml")}
	in := strings.NewReader("vault\nprod\n\n\n\n")
	if err := cmdInitWizard(o, in, io.Discard, noGit, true); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(o.config)
	if err != nil || c.Targets.Vault == nil || c.Environments["prod"] == "" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestInitWizardErrors(t *testing.T) {
	// --repo overrides git; bad target fails rendering.
	dir := t.TempDir()
	o := opts{config: filepath.Join(dir, "env4ci.yaml"), targets: "github", envs: "production", repo: "x/y"}
	if err := cmdInitWizard(o, nil, io.Discard, noGit, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := config.Load(o.config); c.Targets.GitHub.Repo != "x/y" {
		t.Fatal("--repo ignored")
	}
	o2 := opts{config: filepath.Join(t.TempDir(), "env4ci.yaml"), targets: "bitbucket"}
	if err := cmdInitWizard(o2, nil, io.Discard, noGit, false); err == nil {
		t.Fatal("bad target")
	}

	// gen fails: .env4ci exists as a file.
	d3 := t.TempDir()
	os.WriteFile(filepath.Join(d3, ".env4ci"), nil, 0o600)
	o3 := opts{config: filepath.Join(d3, "env4ci.yaml"), targets: "vault", envs: "production"}
	if err := cmdInitWizard(o3, nil, io.Discard, noGit, false); err == nil {
		t.Fatal("gen failure must surface")
	}

	// Makefile fails: Makefile is a directory.
	d4 := t.TempDir()
	os.Mkdir(filepath.Join(d4, "Makefile"), 0o755)
	o4 := opts{config: filepath.Join(d4, "env4ci.yaml"), targets: "vault", envs: "production"}
	if err := cmdInitWizard(o4, nil, io.Discard, noGit, false); err == nil {
		t.Fatal("Makefile failure must surface")
	}
}

func TestSmallHelpers(t *testing.T) {
	if keyLike("") || contains([]string{"a"}, "b") {
		t.Fatal("keyLike/contains")
	}
	if isTerminal(strings.NewReader("")) {
		t.Fatal("reader is not a terminal")
	}
	f, _ := os.CreateTemp(t.TempDir(), "x")
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("regular file is not a terminal")
	}
	if b, err := (gitSource{dir: t.TempDir()}).Branch(); err == nil && b == "" {
		t.Fatal("Branch")
	}
}

func TestPaletteBranches(t *testing.T) {
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("NO_COLOR", "1")
	if newPalette(os.Stdout).on {
		t.Fatal("NO_COLOR ignored")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")
	f, _ := os.CreateTemp(t.TempDir(), "x")
	defer f.Close()
	if newPalette(f).on {
		t.Fatal("file is not a terminal")
	}
	if (palette{on: true}).Purple("x") != "\x1b[35mx\x1b[0m" {
		t.Fatal("Purple")
	}
	if runtime.GOOS != "windows" {
		if tty, err := os.OpenFile("/dev/null", os.O_WRONLY, 0); err == nil {
			defer tty.Close()
			if !newPalette(tty).on {
				t.Fatal("char device should enable color")
			}
		}
	}
}

// --- validate / scan / verify -----------------------------------------

func TestValidateScanVerifyErrors(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("source: "+filepath.Join(dir, "missing.env")+"\n"), 0o600)
	runErr(t, "missing.env", "validate", "-c", cfg)

	bad := filepath.Join(dir, "badci")
	os.MkdirAll(filepath.Join(bad, ".github", "workflows"), 0o755)
	os.WriteFile(filepath.Join(bad, ".github", "workflows", "x.yml"), []byte("jobs: [unclosed"), 0o644)
	runErr(t, "x.yml", "scan", "--dir", bad, "-c", cfg)

	empty := t.TempDir()
	if s := mustRun(t, "scan", "--dir", empty, "-c", cfg); !strings.Contains(s, "No CI variables") {
		t.Fatal(s)
	}

	os.WriteFile(cfg, []byte("rules: [{pattern: x, type: nope}]\n"), 0o600)
	runErr(t, "rules[0]", "scan", "--dir", empty, "-c", cfg)
}

func TestScanCompleteFileAndWriteError(t *testing.T) {
	dir := copyDir(t, filepath.Join("testdata", "project"))
	cfg := filepath.Join(dir, "none.yaml")
	full := filepath.Join(dir, "full.env")
	var all strings.Builder
	for _, k := range []string{"AWS_REGION", "CODECOV_TOKEN", "HOMEBREW_TAP_TOKEN", "LINT_TOKEN", "REGISTRY_PASSWORD", "REGISTRY_USER",
		"SONAR_HOST", "SONAR_TOKEN", "APP_PORT", "DATABASE_URL", "JWT_SECRET", "LOG_LEVEL", "NOTIFY", "SLACK_WEBHOOK"} {
		all.WriteString(k + "=x\n")
	}
	os.WriteFile(full, []byte(all.String()), 0o600)
	if s := mustRun(t, "scan", "--dir", dir, "-f", full, "-c", cfg); !strings.Contains(s, "has every key CI uses") {
		t.Fatal(s)
	}
	runErr(t, "missing.env", "scan", "--dir", dir, "-f", filepath.Join(dir, "missing.env"), "-c", cfg)

	// A directory where the example file should go: write fails (not "exists").
	os.Mkdir(filepath.Join(dir, ".env.example"), 0o755)
	old := openFile
	defer func() { openFile = old }()
	openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errors.New("disk full") }
	runErr(t, "disk full", "scan", "--dir", dir, "--write", "-c", cfg)
}

func TestVerifyBranches(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("branches: { main: production }\n"), 0o600)
	t.Setenv("GITHUB_HEAD_REF", "")
	t.Setenv("GITHUB_REF_NAME", "feature/x")
	runErr(t, `branch "feature/x"`, "verify", "-c", cfg)

	runErr(t, "missing.env", "verify", "-c", cfg, "-e", "production", "-f", filepath.Join(dir, "missing.env"))

	key := parseOnlyKey(t)
	ok := filepath.Join(dir, "ok.env")
	os.WriteFile(ok, []byte("DEPLOY_KEY=\""+key+"\"\n"), 0o600)
	if s := mustRun(t, "verify", "-c", cfg, "-e", "production", "-f", ok); !strings.Contains(s, "✓ ssh DEPLOY_KEY") {
		t.Fatal(s)
	}
	fail := filepath.Join(dir, "fail.env")
	os.WriteFile(fail, []byte("SSH_PRIVATE_KEY=\""+key+"\"\nSSH_HOST=127.0.0.1:1\nSSH_USER=u\nSSH_KNOWN_HOSTS=\"# none\"\n"), 0o600)
	runErr(t, "credential check failed", "verify", "-c", cfg, "-e", "production", "-f", fail)
}

// parseOnlyKey returns a fresh OpenSSH private key with escaped newlines.
func parseOnlyKey(t *testing.T) string {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(pem.EncodeToMemory(block)), "\n", `\n`)
}

type dotenvKey = dotenv.ExampleKey

// --- diff / push / pull ------------------------------------------------

func TestSyncErrorsAndPaths(t *testing.T) {
	dir, cfg, _ := vaultProject(t, map[string]string{"production": "A_TOKEN=12345678\n", "staging": "B_TOKEN=12345678\n"})

	runErr(t, "--all is not supported for pull", "pull", "--all", "-c", cfg)
	runErr(t, "several targets", "diff", "-c", writeCfg(t, "targets: { github: { repo: a/b }, vault: { address: x, path: p } }\n"))

	// One environment's file is missing: --all reports it and fails.
	os.Remove(filepath.Join(dir, "staging.env"))
	runErr(t, "one or more environments failed", "push", "--all", "-y", "-c", cfg)

	// Nothing to do on second push.
	if s := mustRun(t, "push", "-e", "production", "-y", "-c", cfg); !strings.Contains(s, "Nothing to do") {
		t.Fatal(s)
	}

	// Provider without token.
	t.Setenv("VAULT_TOKEN", "")
	old := homeDir
	defer func() { homeDir = old }()
	homeDir = func() (string, error) { return "", errors.New("no home") }
	runErr(t, "vault: no token", "diff", "-e", "production", "-c", cfg)

	// Token from ~/.vault-token.
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".vault-token"), []byte("vtok\n"), 0o600)
	homeDir = func() (string, error) { return home, nil }
	mustRun(t, "diff", "-e", "production", "-c", cfg)

	// Missing ~/.vault-token.
	homeDir = func() (string, error) { return t.TempDir(), nil }
	runErr(t, "vault: no token", "diff", "-e", "production", "-c", cfg)
}

func writeCfg(t *testing.T, y string) string {
	p := filepath.Join(t.TempDir(), "env4ci.yaml")
	os.WriteFile(p, []byte(y), 0o600)
	return p
}

func TestSyncListErrorAndLoadError(t *testing.T) {
	_, cfg, _ := vaultProject(t, map[string]string{"production": "A_TOKEN=12345678\n"})
	t.Setenv("VAULT_TOKEN", "wrong")
	runErr(t, "vault:secret/app/production", "diff", "-e", "production", "-c", cfg)
	t.Setenv("VAULT_TOKEN", "vtok")
	runErr(t, "missing.env", "diff", "-e", "production", "-f", "missing.env", "-c", cfg)
	runErr(t, "repository unknown", "diff", "gitlab", "-c", cfg, "--shared") // git remote is github: no gitlab repo
}

// githubFake serves one secret and one variable and accepts deletes.
func githubFake(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/actions/secrets", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total_count":1,"secrets":[{"name":"S_TOKEN"}]}`)
	})
	mux.HandleFunc("GET /repos/o/r/actions/variables", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total_count":2,"variables":[{"name":"OLD","value":"x"},{"name":"APP_PORT","value":"1"}]}`)
	})
	mux.HandleFunc("DELETE /repos/o/r/actions/variables/OLD", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("DELETE /repos/o/r/actions/secrets/S_TOKEN", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubPlanColorsPrunePull(t *testing.T) {
	srv := githubFake(t)
	dir := t.TempDir()
	cfg := writeCfg(t, "targets: { github: { repo: o/r, base_url: \""+srv.URL+"\" } }\n")
	env := filepath.Join(dir, "p.env")
	os.WriteFile(env, []byte("S_TOKEN=abc\nAPP_PORT=1\n"), 0o600)
	t.Setenv("GITHUB_TOKEN", "t")
	t.Setenv("FORCE_COLOR", "1")

	s := mustRun(t, "diff", "-f", env, "-c", cfg, "--prune")
	for _, want := range []string{"\x1b[35m  ? S_TOKEN", "\x1b[31m  - OLD", "delete"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%q", want, s)
		}
	}
	s = mustRun(t, "diff", "-f", env, "-c", cfg)
	if !strings.Contains(s, "\x1b[2m  - OLD") {
		t.Fatalf("remote-only without prune should be dim:\n%q", s)
	}

	// Only remote-only keys differ: push --prune deletes them without writes.
	os.WriteFile(env, []byte("APP_PORT=1\n"), 0o600)
	t.Setenv("FORCE_COLOR", "")
	if err := run(context.Background(), []string{"diff", "-f", env, "-c", cfg, "--prune", "--exit-code"}, nil, io.Discard); !errors.Is(err, errDrift) {
		t.Fatalf("prune drift: %v", err)
	}
	if s := mustRun(t, "push", "-f", env, "-c", cfg, "--prune", "-y", "--no-verify"); !strings.Contains(s, "0 written, 2 deleted") {
		t.Fatal(s)
	}

	// pull: default file name, hidden secrets reported, gitignored.
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	s = mustRun(t, "pull", "-c", cfg, "--shared")
	if !strings.Contains(s, "to .env.pulled") || !strings.Contains(s, "1 secrets are write-only") || !strings.Contains(s, "added .env.pulled to .gitignore") {
		t.Fatal(s)
	}
	runErr(t, "refusing to overwrite", "pull", "-c", cfg, "--shared")
	runErr(t, "env4ci config", "pull", "-c", cfg, "-o", cfg)
}

func TestPullDefaultEnvFileName(t *testing.T) {
	dir, cfg, _ := vaultProject(t, map[string]string{"production": "A_TOKEN=12345678\n"})
	mustRun(t, "push", "-e", "production", "-y", "-c", cfg)
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	if s := mustRun(t, "pull", "-e", "production", "-c", cfg); !strings.Contains(s, "to .env.production") {
		t.Fatal(s)
	}
}

func TestPullListError(t *testing.T) {
	_, cfg, _ := vaultProject(t, map[string]string{"production": "A=1\n"})
	t.Setenv("VAULT_TOKEN", "wrong")
	runErr(t, "403", "pull", "-e", "production", "-o", filepath.Join(t.TempDir(), "x.env"), "-c", cfg)
}

func TestNewProviderTokens(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	old := ghTokenCmd
	defer func() { ghTokenCmd = old }()
	ghTokenCmd = func(context.Context) ([]byte, error) { return nil, errors.New("gh missing") }
	if _, err := newProvider(target{Provider: "github", Repo: "o/r"}); err == nil {
		t.Fatal("github without token")
	}
	ghTokenCmd = func(context.Context) ([]byte, error) { return []byte("gho_x\n"), nil }
	if p, err := newProvider(target{Provider: "github", Repo: "o/r"}); err != nil || p == nil {
		t.Fatalf("gh token: %v", err)
	}
	t.Setenv("GITLAB_TOKEN", "")
	if _, err := newProvider(target{Provider: "gitlab", Repo: "g/p"}); err == nil {
		t.Fatal("gitlab without token")
	}
	if _, err := newProvider(target{Provider: "x"}); err == nil {
		t.Fatal("unknown provider")
	}
	if b, err := old(context.Background()); err == nil && len(b) == 0 {
		t.Log("gh present but empty token") // real gh: just exercise the default seam
	}
}

func TestResolveTargetMoreBranches(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	cfg := config.Config{Targets: config.Targets{GitHub: &config.GitHub{Repo: "o/r", Environment: "prod", BaseURL: "https://ghe/api/v3"}}}
	tg, err := resolveTarget(cfg, opts{}, nil, noGit)
	if err != nil || tg.Environment != "prod" || tg.BaseURL != "https://ghe/api/v3" {
		t.Fatalf("%+v %v", tg, err)
	}
	if _, err := resolveTarget(config.Config{}, opts{}, []string{"vault"}, noGit); err == nil || !strings.Contains(err.Error(), "address unknown") {
		t.Fatalf("vault nil config: %v", err)
	}
	cfg = config.Config{Targets: config.Targets{Vault: &config.Vault{Address: "x"}}}
	if _, err := resolveTarget(cfg, opts{}, nil, noGit); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("vault no path: %v", err)
	}
}

func TestCredentialChecksConfigSSHAndAllPass(t *testing.T) {
	key := strings.ReplaceAll(parseOnlyKey(t), `\n`, "\n")
	cfg := config.Config{Checks: config.Checks{SSH: []config.SSHCheck{{Key: "MY_KEY"}}}}
	cs := credentialChecks(cfg, []domain.Variable{{Key: "MY_KEY", Value: key}})
	if len(cs) != 1 {
		t.Fatalf("%d checks", len(cs))
	}
	var out bytes.Buffer
	if err := runCredentialChecks(context.Background(), &out, cs, nil); err != nil || !strings.Contains(out.String(), "✓ ssh MY_KEY") {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if err := runCredentialChecks(context.Background(), io.Discard, []application.Check{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLocalBranches(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s.env")
	os.WriteFile(src, []byte("A=1\n"), 0o600)
	if vs, err := loadLocal(config.Config{Source: src}, opts{}); err != nil || len(vs) != 1 {
		t.Fatalf("source: %v %v", vs, err)
	}
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	if _, err := loadLocal(config.Config{}, opts{}); err == nil { // ./.env missing
		t.Fatal("default .env")
	}
	os.WriteFile(".env", []byte("NOEQ\n"), 0o600)
	if _, err := loadLocal(config.Config{}, opts{}); err == nil {
		t.Fatal("parse error")
	}
	os.WriteFile(".env", []byte("A=1\n"), 0o600)
	if _, err := loadLocal(config.Config{Rules: []config.Rule{{Pattern: "*", Type: "bad"}}}, opts{}); err == nil {
		t.Fatal("classifier error")
	}
	os.WriteFile(".env", []byte("BAD-KEY=1\n"), 0o600)
	if _, err := loadLocal(config.Config{}, opts{}); err == nil {
		t.Fatal("invalid key")
	}
}

func TestEnsureGitignoredErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureGitignored(dir, "x"); err == nil { // .gitignore path is a directory
		t.Fatal("read error")
	}
	if _, err := ensureGitignored(filepath.Join(dir, "no", "such", ".gitignore"), "x"); err == nil {
		t.Fatal("open error")
	}
}

// --- gen -------------------------------------------------------------

func TestLoadAuthFileErrors(t *testing.T) {
	if err := loadAuthFile(config.Config{AuthFile: "bad\x00name"}, io.Discard); err == nil {
		t.Fatal("open error")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "a.env")
	os.WriteFile(bad, []byte("NOEQ\n"), 0o600)
	if err := loadAuthFile(config.Config{AuthFile: bad}, io.Discard); err == nil {
		t.Fatal("parse error")
	}
	if runtime.GOOS != "windows" {
		open := filepath.Join(dir, "open.env")
		os.WriteFile(open, []byte("VAULT_NAMESPACE=\n"), 0o644)
		var out bytes.Buffer
		loadAuthFile(config.Config{AuthFile: open}, &out)
		if !strings.Contains(out.String(), "chmod 600") {
			t.Fatalf("perm warning: %q", out.String())
		}
	}
}

func TestGenErrors(t *testing.T) {
	newCfg := func(y string) (string, string) {
		dir := t.TempDir()
		p := filepath.Join(dir, "env4ci.yaml")
		os.WriteFile(p, []byte(y), 0o600)
		return dir, p
	}
	// Auth folder blocked by a file.
	dir, cfg := newCfg("auth_file: blk/a.env\ntargets: { vault: { address: x, path: p } }\n")
	os.WriteFile(filepath.Join(dir, "blk"), nil, 0o600)
	runErr(t, "", "gen", "-c", cfg, "--dir", dir)

	// Auth file write fails.
	dir, cfg = newCfg("targets: { vault: { address: x, path: p } }\n")
	old := openFile
	openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errors.New("disk full") }
	runErr(t, "disk full", "gen", "-c", cfg, "--dir", dir)
	openFile = old

	// No environments: Source (default .env) is used.
	dir, cfg = newCfg("targets: { vault: { address: x, path: p } }\n")
	mustRun(t, "gen", "-c", cfg, "--dir", dir)
	if _, err := os.Stat(filepath.Join(dir, ".env")); err != nil {
		t.Fatal("default .env not created")
	}

	// Bad rule.
	dir, cfg = newCfg("rules: [{pattern: x, type: bad}]\ntargets: { vault: { address: x, path: p } }\n")
	runErr(t, "rules[0]", "gen", "-c", cfg, "--dir", dir)

	// Bad CI file.
	dir, cfg = newCfg("targets: { vault: { address: x, path: p } }\n")
	os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755)
	os.WriteFile(filepath.Join(dir, ".github", "workflows", "x.yml"), []byte("jobs: [x"), 0o644)
	runErr(t, "x.yml", "gen", "-c", cfg, "--dir", dir)

	// Environment uses the auth file.
	dir, cfg = newCfg("environments: { p: .env.env4ci }\ntargets: { vault: { address: x, path: p } }\n")
	runErr(t, "env4ci auth file", "gen", "-c", cfg, "--dir", dir)

	// Environment folder blocked; environment file write fails.
	dir, cfg = newCfg("environments: { p: blk/p.env }\ntargets: { vault: { address: x, path: p } }\n")
	os.WriteFile(filepath.Join(dir, "blk"), nil, 0o600)
	runErr(t, "", "gen", "-c", cfg, "--dir", dir)
	dir, cfg = newCfg("environments: { p: p.env }\ntargets: { vault: { address: x, path: p } }\n")
	calls := 0
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("disk full")
		}
		return old(name, flag, perm)
	}
	runErr(t, "disk full", "gen", "-c", cfg, "--dir", dir)
	openFile = old
}

func TestGenWarnsWhenConfigIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	c := exec.Command("git", "init", "-q")
	c.Dir = dir
	if err := c.Run(); err != nil {
		t.Skip("git init failed")
	}
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("env4ci.yaml\n"), 0o644)
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte("targets: { vault: { address: x, path: p } }\n"), 0o600)
	if s := mustRun(t, "gen", "-c", cfg, "--dir", dir); !strings.Contains(s, "is gitignored") {
		t.Fatal(s)
	}
}

func TestWriteOrAppendErrors(t *testing.T) {
	dir := t.TempDir()
	if err := writeOrAppend(dir, "h", nil, io.Discard, palette{}); err == nil { // path is a directory
		t.Fatal("read error")
	}
	bad := filepath.Join(dir, "bad.env")
	os.WriteFile(bad, []byte("NOEQ\n"), 0o600)
	if err := writeOrAppend(bad, "h", []dotenvKey{{Key: "A"}}, io.Discard, palette{}); err == nil {
		t.Fatal("parse error")
	}
	ok := filepath.Join(dir, "ok.env")
	os.WriteFile(ok, []byte("B=1\n"), 0o600)
	old := openFile
	defer func() { openFile = old }()
	openFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errors.New("disk full") }
	if err := writeOrAppend(ok, "h", []dotenvKey{{Key: "A"}}, io.Discard, palette{}); err == nil {
		t.Fatal("append error")
	}
}

func TestProtectAndGitignoreErrors(t *testing.T) {
	base := t.TempDir()
	os.Mkdir(filepath.Join(base, ".gitignore"), 0o755) // cannot be read as a file
	if err := protect(base, "top.env", io.Discard, palette{}); err == nil {
		t.Fatal("gitignore error")
	}
	if err := protect(base, "dir/x.env", io.Discard, palette{}); err == nil {
		t.Fatal("gitignore error for folder")
	}
}

func TestEnvFileKeysDedup(t *testing.T) {
	keys := envFileKeys("p", []domain.Reference{{Key: "A", Environment: "p"}, {Key: "A"}})
	if len(keys) != 1 {
		t.Fatalf("%+v", keys)
	}
}

func TestMakefileWriteFailure(t *testing.T) {
	unixOnly(t)
	dir := t.TempDir()
	mk := filepath.Join(dir, "Makefile")
	os.WriteFile(mk, []byte("a:\n\ttrue\n"), 0o444)
	if os.Geteuid() == 0 {
		t.Skip("root can write read-only files")
	}
	if err := writeMakefile(mk, io.Discard); err == nil {
		t.Fatal("want write error")
	}
}

func TestRunInitWithFlags(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	if s := mustRun(t, "init", "--targets", "vault", "--envs", "production", "-c", cfg); !strings.Contains(s, "wrote") {
		t.Fatal(s)
	}
}

func TestScanWriteFailsAfterOpen(t *testing.T) {
	dir := copyDir(t, filepath.Join("testdata", "project"))
	old := openFile
	defer func() { openFile = old }()
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		f, err := old(name, flag, perm)
		if err == nil {
			f.Close() // the example write will fail
		}
		return f, err
	}
	runErr(t, "closed", "scan", "--dir", dir, "--write", "-c", filepath.Join(dir, "none.yaml"))
}

func TestDiffPruneInSync(t *testing.T) {
	_, cfg, _ := vaultProject(t, map[string]string{"production": "A_TOKEN=12345678\n"})
	mustRun(t, "push", "-e", "production", "-y", "-c", cfg)
	if err := run(context.Background(), []string{"diff", "-e", "production", "--prune", "--exit-code", "-c", cfg}, nil, io.Discard); err != nil {
		t.Fatalf("in sync with --prune: %v", err)
	}
	if s := mustRun(t, "push", "-e", "production", "--prune", "-y", "-c", cfg); !strings.Contains(s, "Nothing to do") {
		t.Fatal(s)
	}
}

func TestProtectFolderErrors(t *testing.T) {
	base := t.TempDir()
	os.WriteFile(filepath.Join(base, "blk"), nil, 0o600)
	if err := protect(base, "blk/a.env", io.Discard, palette{}); err == nil || !strings.Contains(err.Error(), "cannot create folder") {
		t.Fatalf("folder is a file: %v", err)
	}
	if err := protect(base, "blk/sub/a.env", io.Discard, palette{}); err == nil {
		t.Fatal("parent is a file: want error")
	}
}
