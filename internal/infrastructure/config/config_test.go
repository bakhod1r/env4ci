package config

import (
	"os"
	"path/filepath"
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
