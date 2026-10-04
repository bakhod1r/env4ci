package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
)

func TestRenderInitGolden(t *testing.T) {
	cases := map[string]initPlan{
		"init-single-github.yaml": {
			Targets: []string{"github"}, Environments: []string{"production"}, Repo: "acme/api",
			VaultMount: "secret", VaultPath: "api/{env}",
		},
		"init-multi-all.yaml": {
			Targets: []string{"github", "gitlab", "vault"}, Environments: []string{"production", "staging", "dev"},
			Repo: "acme/api", VaultAddr: "https://vault.acme.io:8200", VaultMount: "kv", VaultPath: "api/{env}",
		},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := renderInit(p)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, name, string(b))
		})
	}
}

func TestRenderInitLoadsAsConfig(t *testing.T) {
	b, err := renderInit(initPlan{Targets: []string{"vault", "gitlab"}, Environments: []string{"production", "staging"}, Repo: "g/p", VaultMount: "secret", VaultPath: "p/{env}"})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, b, 0o600)
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Branches["main"] != "production" || c.Branches["develop"] != "staging" ||
		c.Environments["staging"] != ".gitlab/staging.env" || c.AuthFile != ".gitlab/env4ci.env" || c.Targets.Vault.Path != "p/{env}" ||
		c.Targets.GitLab.Project != "g/p" || !c.Targets.GitLab.Protected || c.Targets.GitHub != nil {
		t.Fatalf("%+v", c)
	}
}

func TestRenderInitErrors(t *testing.T) {
	for want, p := range map[string]initPlan{
		"at least one target":      {Environments: []string{"production"}},
		"at least one environment": {Targets: []string{"github"}},
		"unknown target":           {Targets: []string{"bitbucket"}, Environments: []string{"production"}},
		"both map to branch":       {Targets: []string{"github"}, Environments: []string{"production", "prod"}},
		"letters, digits":          {Targets: []string{"github"}, Environments: []string{"pro duction!"}},
	} {
		if _, err := renderInit(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v", want, err)
		}
	}
}

func TestAskInit(t *testing.T) {
	in := strings.NewReader("github, vault\nproduction,staging\n\nhttps://v:8200\n\nsvc/{env}\n")
	var out bytes.Buffer
	got := askInit(in, &out, initPlan{Targets: []string{"github"}, Environments: []string{"production"}, Repo: "acme/api", VaultMount: "secret", VaultPath: "api/{env}"})
	if strings.Join(got.Targets, ",") != "github,vault" || strings.Join(got.Environments, ",") != "production,staging" ||
		got.Repo != "acme/api" || got.VaultAddr != "https://v:8200" || got.VaultMount != "secret" || got.VaultPath != "svc/{env}" {
		t.Fatalf("got %+v\nprompts:\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "Repository (owner/name or group/project) [acme/api]") {
		t.Fatalf("prompts:\n%s", out.String())
	}
}

func TestInitWizardWithFlags(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "env4ci.yaml")
	os.MkdirAll(filepath.Join(dir, ".gitlab"), 0o755)
	os.WriteFile(filepath.Join(dir, ".gitlab", "production.env"), []byte("KEEP=1\n"), 0o600)
	var out bytes.Buffer
	o := opts{config: cfg, targets: "gitlab,vault", envs: "production,staging"}
	if err := cmdInitWizard(o, nil, &out, fakeGit{remote: ghRemote}, false); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Targets.GitLab.Project != "acme/api" || c.Targets.Vault.Path != "api/{env}" {
		t.Fatalf("%+v", c.Targets)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitlab", "production.env")); !strings.HasPrefix(string(b), "KEEP=1\n") {
		t.Fatal("existing .env overwritten")
	}
	if st, err := os.Stat(filepath.Join(dir, ".gitlab", "staging.env")); err != nil || (st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("staging.env: %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitlab", "env4ci.env")); err != nil {
		t.Fatal("auth file missing")
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(gi) != "/.gitlab/env4ci.env\n/.gitlab/production.env\n/.gitlab/staging.env\n" {
		t.Fatalf(".gitignore:\n%s", gi)
	}
	// Refuses to overwrite.
	if err := cmdInitWizard(o, nil, &out, fakeGit{remote: ghRemote}, false); err == nil {
		t.Fatal("want exists error")
	}
}

func TestInitPlanConfigMatchesYAML(t *testing.T) {
	for _, p := range []initPlan{
		{Targets: []string{"github", "gitlab", "vault"}, Environments: []string{"production", "staging"}, Repo: "a/b", VaultAddr: "https://v", VaultMount: "kv", VaultPath: "b/{env}"},
		{Targets: []string{"github", "gitlab", "vault"}, Environments: []string{"dev"}, VaultMount: "secret", VaultPath: "app/{env}"},
	} {
		b, err := renderInit(p)
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(t.TempDir(), "c.yaml")
		os.WriteFile(f, b, 0o600)
		loaded, err := config.Load(f)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(loaded, p.config()) {
			t.Fatalf("yaml  %+v\nconfig %+v", loaded, p.config())
		}
	}
}

func TestSecretsDirFor(t *testing.T) {
	for _, tc := range []struct {
		targets []string
		remote  string
		want    string
	}{
		{[]string{"github"}, "", ".github"},
		{[]string{"gitlab"}, "", ".gitlab"},
		{[]string{"github", "gitlab"}, "gitlab", ".gitlab"},
		{[]string{"github", "gitlab"}, "github", ".github"},
		{[]string{"gitlab", "vault"}, "github", ".gitlab"},
		{[]string{"vault"}, "github", ""},
	} {
		if got := secretsDirFor(tc.targets, tc.remote); got != tc.want {
			t.Errorf("%v remote=%q: %q want %q", tc.targets, tc.remote, got, tc.want)
		}
	}
}
