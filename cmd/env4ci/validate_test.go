package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValueRulesBlockPushAndValidate(t *testing.T) {
	srv, store := fakeVault(t)
	dir := t.TempDir()
	env := filepath.Join(dir, "prod.env")
	os.WriteFile(env, []byte("DATABASE_URL=mysql://secret-host/db\nAPP_PORT=99999\n"), 0o600)
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte(fmt.Sprintf(`environments: { production: %q }
validate:
  DATABASE_URL: { pattern: "^postgres://" }
  APP_PORT: { type: port }
  SENTRY_DSN: { required_in: [production] }
targets:
  vault: { address: %q, path: "api/{env}" }
`, env, srv.URL)), 0o600)
	t.Setenv("VAULT_TOKEN", "vtok")
	t.Setenv("VAULT_ADDR", "")

	var out bytes.Buffer
	err := run(context.Background(), []string{"push", "-c", cfg, "-e", "production", "-y"}, nil, &out)
	if err == nil || !strings.Contains(err.Error(), "3 value(s) failed") || len(store) != 0 {
		t.Fatalf("err = %v, store = %v\n%s", err, store, out.String())
	}
	for _, want := range []string{"APP_PORT: not a port", "DATABASE_URL: does not match ^postgres://", "SENTRY_DSN: required in production"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "secret-host") {
		t.Fatalf("value printed:\n%s", out.String())
	}

	out.Reset()
	if err := run(context.Background(), []string{"validate", "-c", cfg, "-f", env, "-e", "production"}, nil, &out); err == nil {
		t.Fatalf("validate should fail:\n%s", out.String())
	}

	os.WriteFile(env, []byte("DATABASE_URL=postgres://h/db\nAPP_PORT=80\nSENTRY_DSN=https://x@sentry.io/1\n"), 0o600)
	if err := run(context.Background(), []string{"push", "-c", cfg, "-e", "production", "-y"}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	os.WriteFile(cfg, []byte("validate: { A: { type: uuid } }\n"), 0o600)
	if err := run(context.Background(), []string{"validate", "-c", cfg, "-f", env}, nil, &out); err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("bad rule: %v", err)
	}
}
