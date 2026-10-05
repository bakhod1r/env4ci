package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/iotest"
)

// rotateSetup: vault target in a temp project (cwd), prod.env with two keys.
func rotateSetup(t *testing.T, extra string) (string, map[string]map[string]any) {
	t.Helper()
	srv, store := fakeVault(t)
	t.Chdir(t.TempDir())
	os.WriteFile("prod.env", []byte("# comment\nAPI_TOKEN=old-token-value\nAPP_PORT=80\n"), 0o600)
	os.WriteFile("env4ci.yaml", []byte(fmt.Sprintf("environments: { production: prod.env }\n%s\ntargets:\n  vault: { address: %q, path: \"api/{env}\" }\n", extra, srv.URL)), 0o600)
	t.Setenv("VAULT_TOKEN", "vtok")
	t.Setenv("VAULT_ADDR", "")
	// Seed the remote through the fake (it tracks versions) with an extra key.
	os.WriteFile("seed.env", []byte("API_TOKEN=old-token-value\nAPP_PORT=80\nREMOTE_ONLY=r\n"), 0o600)
	if err := run(context.Background(), []string{"push", "-e", "production", "-f", "seed.env", "-y"}, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	return "prod.env", store
}

func noTerminal(t *testing.T) {
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return false }
	t.Cleanup(func() { stdinIsTerminal = old })
}

func TestRotateGenerate(t *testing.T) {
	noTerminal(t)
	env, store := rotateSetup(t, "")
	var out bytes.Buffer
	if err := run(context.Background(), []string{"rotate", "API_TOKEN", "-e", "production", "--generate", "-y"}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	b, _ := os.ReadFile(env)
	s := string(b)
	if !strings.HasPrefix(s, "# comment\nAPI_TOKEN=\"") || !strings.HasSuffix(s, "\"\nAPP_PORT=80\n") || strings.Contains(s, "old-token-value") {
		t.Fatalf("env file:\n%s", s)
	}
	newVal := strings.TrimSuffix(strings.TrimPrefix(strings.Split(s, "\n")[1], "API_TOKEN=\""), "\"")
	if len(newVal) != 43 || store["api/production"]["API_TOKEN"] != newVal {
		t.Fatalf("new = %q, store = %v", newVal, store)
	}
	if bak, _ := os.ReadFile(env + ".bak"); !strings.Contains(string(bak), "old-token-value") {
		t.Fatalf("backup: %s", bak)
	}
	if gi, _ := os.ReadFile(".gitignore"); !strings.Contains(string(gi), "prod.env.bak") {
		t.Fatalf(".gitignore: %s", gi)
	}
	if strings.Contains(out.String(), newVal) || strings.Contains(out.String(), "old-token-value") {
		t.Fatalf("value printed:\n%s", out.String())
	}
}

func TestRotateOnlyTouchesKey(t *testing.T) {
	noTerminal(t)
	_, store := rotateSetup(t, "")
	in := strings.NewReader("brand-new-token\n")
	if err := run(context.Background(), []string{"rotate", "API_TOKEN", "-e", "production", "-y"}, in, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := store["api/production"]
	if got["API_TOKEN"] != "brand-new-token" || got["APP_PORT"] != "80" || got["REMOTE_ONLY"] != "r" {
		t.Fatalf("store = %v", got)
	}
}

func TestRotateSSHKey(t *testing.T) {
	noTerminal(t)
	t.Chdir(t.TempDir())
	srv, store := fakeVault(t)
	os.WriteFile("prod.env", []byte("DEPLOY_SSH_KEY=\"-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\"\n"), 0o600)
	os.WriteFile("env4ci.yaml", []byte(fmt.Sprintf("environments: { production: prod.env }\ntargets:\n  vault: { address: %q, path: \"api/{env}\" }\n", srv.URL)), 0o600)
	t.Setenv("VAULT_TOKEN", "vtok")
	t.Setenv("VAULT_ADDR", "")

	// Not confirmed: nothing written.
	var out bytes.Buffer
	err := run(context.Background(), []string{"rotate", "DEPLOY_SSH_KEY", "-e", "production", "--generate"}, strings.NewReader("n\n"), &out)
	if err == nil || err.Error() != "aborted" || !strings.Contains(out.String(), "ssh-ed25519 ") || len(store) != 0 {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "PRIVATE KEY") {
		t.Fatalf("private key printed:\n%s", out.String())
	}
	// --no-verify -y: written; the new key parses.
	out.Reset()
	if err := run(context.Background(), []string{"rotate", "DEPLOY_SSH_KEY", "-e", "production", "--generate", "--no-verify", "-y"}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	v, _ := store["api/production"]["DEPLOY_SSH_KEY"].(string)
	if !strings.HasPrefix(v, "-----BEGIN OPENSSH PRIVATE KEY-----") || strings.Contains(v, "\nAAAA\n") {
		t.Fatalf("stored = %q", v)
	}
	// Confirmed but the login check fails (no host): nothing more written, file unchanged.
	before, _ := os.ReadFile("prod.env")
	os.WriteFile("prod.env", append(before, []byte("DEPLOY_HOST=127.0.0.1\nDEPLOY_USER=u\nDEPLOY_PORT=1\n")...), 0o600)
	before, _ = os.ReadFile("prod.env")
	err = run(context.Background(), []string{"rotate", "DEPLOY_SSH_KEY", "-e", "production", "--generate"}, strings.NewReader("y\n"), &out)
	after, _ := os.ReadFile("prod.env")
	if err == nil || string(before) != string(after) {
		t.Fatalf("err = %v, file changed = %v", err, string(before) != string(after))
	}
}

func TestRotateTerminalPrompt(t *testing.T) {
	_, store := rotateSetup(t, "")
	oldT, oldR := stdinIsTerminal, readHidden
	defer func() { stdinIsTerminal, readHidden = oldT, oldR }()
	stdinIsTerminal = func(io.Reader) bool { return true }
	readHidden = func() ([]byte, error) { return []byte("typed-token-1\n"), nil }
	var out bytes.Buffer
	if err := run(context.Background(), []string{"rotate", "API_TOKEN", "-e", "production"}, strings.NewReader("y\n"), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if store["api/production"]["API_TOKEN"] != "typed-token-1" || !strings.Contains(out.String(), "(hidden)") {
		t.Fatalf("store = %v\n%s", store, out.String())
	}
	readHidden = func() ([]byte, error) { return nil, errors.New("tty gone") }
	if err := run(context.Background(), []string{"rotate", "API_TOKEN", "-e", "production"}, strings.NewReader(""), &out); err == nil || err.Error() != "tty gone" {
		t.Fatalf("err = %v", err)
	}
	// Declining the final confirmation leaves everything as it was.
	readHidden = func() ([]byte, error) { return []byte("another-token"), nil }
	if err := run(context.Background(), []string{"rotate", "API_TOKEN", "-e", "production"}, strings.NewReader("n\n"), &out); err == nil || err.Error() != "aborted" {
		t.Fatalf("err = %v", err)
	}
	if store["api/production"]["API_TOKEN"] != "typed-token-1" {
		t.Fatal("declined rotate wrote")
	}
}

func TestRotateErrors(t *testing.T) {
	noTerminal(t)
	env, store := rotateSetup(t, "validate: { API_TOKEN: { min_len: 12 } }\naudit_log: audit.jsonl")
	ctx := context.Background()
	expect := func(want string, in string, args ...string) {
		t.Helper()
		err := run(ctx, append([]string{"rotate"}, args...), strings.NewReader(in), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%v: err = %v, want %q", args, err, want)
		}
	}
	expect("usage", "")
	expect("usage", "", "A", "vault", "extra")
	expect("NOPE is not in prod.env", "", "NOPE", "-e", "production")
	expect("needs -y", "x\n", "API_TOKEN", "-e", "production")
	expect("empty value", "\n", "API_TOKEN", "-e", "production", "-y")
	expect("equals the current one", "old-token-value", "API_TOKEN", "-e", "production", "-y")
	expect("failed validate", "short", "API_TOKEN", "-e", "production", "-y")
	expect("unknown provider", "", "API_TOKEN", "nope")
	expect("refusing to rotate", "", "API_TOKEN", "-f", ".env.env4ci")
	os.WriteFile("bad.env", []byte("bad line\n"), 0o600)
	expect("bad.env", "", "API_TOKEN", "-f", "bad.env")
	expect("missing.env", "", "API_TOKEN", "-f", "missing.env")

	// Push fails after the local write: error says where the old value is; audit logs it.
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	expect("no token", "long-enough-token", "API_TOKEN", "-e", "production", "-y")
	if b, _ := os.ReadFile(env); !strings.Contains(string(b), "old-token-value") {
		t.Fatal("file written before provider was ready")
	}
	_ = store
}

func TestRotateFailures(t *testing.T) {
	noTerminal(t)
	ctx := context.Background()
	rotate := func(in io.Reader) error {
		return run(ctx, []string{"rotate", "API_TOKEN", "-e", "production", "-y"}, in, &bytes.Buffer{})
	}
	val := func() io.Reader { return strings.NewReader("brand-new-token") }

	// Server: fail the Nth GET, or every POST.
	setup := func(failGet int, failPost bool) {
		t.Helper()
		srv, _ := fakeVault(t)
		inner := srv.Config.Handler
		gets := 0
		srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				gets++
				if gets == failGet {
					w.WriteHeader(500)
					return
				}
			}
			if r.Method == http.MethodPost && failPost {
				w.WriteHeader(500)
				return
			}
			inner.ServeHTTP(w, r)
		})
		t.Chdir(t.TempDir())
		os.WriteFile("prod.env", []byte("API_TOKEN=old-token-value\n"), 0o600)
		os.WriteFile("env4ci.yaml", []byte(fmt.Sprintf("environments: { production: prod.env }\ntargets:\n  vault: { address: %q, path: \"api/{env}\" }\n", srv.URL)), 0o600)
		t.Setenv("VAULT_TOKEN", "vtok")
		t.Setenv("VAULT_ADDR", "")
	}

	setup(0, false)
	if err := rotate(iotest.ErrReader(errors.New("read boom"))); err == nil || !strings.Contains(err.Error(), "read boom") {
		t.Fatalf("stdin error: %v", err)
	}
	setup(1, false)
	if err := rotate(val()); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("list error: %v", err)
	}
	setup(2, false)
	if err := rotate(val()); err == nil || !strings.Contains(err.Error(), "list") {
		t.Fatalf("plan error: %v", err)
	}
	setup(0, true)
	if err := rotate(val()); err == nil || !strings.Contains(err.Error(), "old one is in prod.env.bak") {
		t.Fatalf("apply error: %v", err)
	}

	setup(0, false)
	os.Mkdir(".gitignore", 0o755)
	if err := rotate(val()); err == nil {
		t.Fatal(".gitignore is a folder: want error")
	}
	setup(0, false)
	os.Mkdir("prod.env.bak", 0o755)
	if err := rotate(val()); err == nil {
		t.Fatal("backup is a folder: want error")
	}
	setup(0, false)
	os.Chmod("prod.env", 0o400)
	if err := rotate(val()); err == nil {
		t.Fatal("read-only env file: want error")
	}
}

func TestReadHiddenWithoutTerminal(t *testing.T) {
	if _, err := readHidden(); err == nil {
		t.Skip("stdin is a terminal")
	}
}
