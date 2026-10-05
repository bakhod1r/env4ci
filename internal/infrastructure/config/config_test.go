package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func TestLoadTemplate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte(Template), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != ".env" || c.Default != "secret" {
		t.Fatalf("%+v", c)
	}
	cl, err := c.Classifier()
	if err != nil {
		t.Fatal(err)
	}
	if cl.Classify("APP_PORT") != domain.KindVariable || cl.Classify("X") != domain.KindSecret {
		t.Fatal("classifier mismatch")
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("token: abc\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("want error")
	}
}

func TestBadRuleType(t *testing.T) {
	c := Config{Rules: []Rule{{Pattern: "*", Type: "nope"}}}
	if _, err := c.Classifier(); err == nil {
		t.Fatal("want error")
	}
}

func TestLoadBranchesEnvironmentsChecks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte(`
branches: { main: production, "release/*": staging }
environments: { production: .env.prod }
checks:
  ssh: [{ key: K, host: H, user: U }]
  registry: [{ registry: ghcr.io, username: GU, password: GP }]
`), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Branches["release/*"] != "staging" || c.Environments["production"] != ".env.prod" ||
		c.Checks.SSH[0].Host != "H" || c.Checks.Registry[0].Registry != "ghcr.io" {
		t.Fatalf("%+v", c)
	}
}

func TestValueRules(t *testing.T) {
	load := func(body string) Config {
		t.Helper()
		p := filepath.Join(t.TempDir(), "env4ci.yaml")
		os.WriteFile(p, []byte(body), 0o600)
		c, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := load(`validate:
  DATABASE_URL: { required: true, type: url, pattern: "^postgres://" }
  SENTRY_DSN: { required_in: [production] }
  JWT_SECRET: { min_len: 32, max_len: 64 }
  LOG_LEVEL: { one_of: [debug, info] }
`)
	r, err := c.ValueRules()
	if err != nil || len(r) != 4 || !r["DATABASE_URL"].Required || r["DATABASE_URL"].Pattern.String() != "^postgres://" ||
		r["SENTRY_DSN"].RequiredIn[0] != "production" || r["JWT_SECRET"].MaxLen != 64 || len(r["LOG_LEVEL"].OneOf) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	for body, want := range map[string]string{
		"validate: { A: { type: uuid } }":             "unknown type",
		"validate: { A: { pattern: \"(\" } }":         "validate.A: pattern",
		"validate: { A: { min_len: 9, max_len: 3 } }": "min_len 9 > max_len 3",
	} {
		if _, err := load(body).ValueRules(); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v", body, err)
		}
	}
}
