package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func TestPushWritesAuditLog(t *testing.T) {
	srv, _ := fakeVault(t)
	dir := t.TempDir()
	env := filepath.Join(dir, "prod.env")
	os.WriteFile(env, []byte("DATABASE_URL=postgres://secret-value\n"), 0o600)
	log := filepath.Join(dir, "audit", "env4ci.jsonl")
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte(fmt.Sprintf("audit_log: %q\nenvironments: { production: %q }\ntargets:\n  vault: { address: %q, path: \"api/{env}\" }\n", log, env, srv.URL)), 0o600)
	t.Setenv("VAULT_TOKEN", "vtok")
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("GITHUB_ACTOR", "octocat")
	old := now
	now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.FixedZone("x", 5*3600)) }
	defer func() { now = old }()

	var out bytes.Buffer
	if err := run(context.Background(), []string{"push", "-c", cfg, "-e", "production", "-y"}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-value") {
		t.Fatalf("value in audit log: %s", b)
	}
	var e domain.AuditEntry
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if e.Actor != "octocat" || e.Provider != "vault" || e.Target != "api/production" || e.Environment != "production" ||
		e.Result != "ok" || len(e.Changes) != 1 || e.Changes[0].Key != "DATABASE_URL" || e.Changes[0].Action != "create" ||
		!e.Time.Equal(time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC)) || e.Time.Location() != time.UTC {
		t.Fatalf("entry = %+v", e)
	}

	// Unwritable log: the push still succeeds, with a warning.
	os.WriteFile(env, []byte("DATABASE_URL=postgres://changed\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "blk"), nil, 0o644)
	os.WriteFile(cfg, []byte(fmt.Sprintf("audit_log: %q\nenvironments: { production: %q }\ntargets:\n  vault: { address: %q, path: \"api/{env}\" }\n",
		filepath.Join(dir, "blk", "x.jsonl"), env, srv.URL)), 0o600)
	out.Reset()
	if err := run(context.Background(), []string{"push", "-c", cfg, "-e", "production", "-y"}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "! audit log") {
		t.Fatalf("no warning:\n%s", out.String())
	}
}

func TestAuditLogRecordsFailedPush(t *testing.T) {
	srv, _ := fakeVault(t)
	dir := t.TempDir()
	env := filepath.Join(dir, "prod.env")
	os.WriteFile(env, []byte("A_KEY=value-1\n"), 0o600)
	log := filepath.Join(dir, "a.jsonl")
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfg, []byte(fmt.Sprintf("audit_log: %q\ntargets:\n  vault: { address: %q, path: \"api/{env}\" }\n", log, srv.URL)), 0o600)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "vtok")
	// Plan with a good token, then the server rejects the write.
	calls := 0
	srv.Config.Handler = wrapAfter(srv.Config.Handler, &calls)
	run(context.Background(), []string{"push", "-c", cfg, "-f", env, "-e", "production", "-y"}, nil, &bytes.Buffer{})
	b, _ := os.ReadFile(log)
	var e domain.AuditEntry
	if err := json.Unmarshal(b, &e); err != nil || e.Result == "ok" || e.Result == "" {
		t.Fatalf("entry = %+v (%v)\n%s", e, err, b)
	}
}

// wrapAfter fails every write (POST) with 500; reads pass through.
func wrapAfter(h http.Handler, calls *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if r.Method == http.MethodPost {
			w.WriteHeader(500)
			return
		}
		h.ServeHTTP(w, r)
	})
}
