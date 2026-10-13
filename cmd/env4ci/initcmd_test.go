package main

import (
	"bytes"
	"errors"
	"io"
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
	in := strings.NewReader("github, vault\n\nhttps://v:8200\n\nsvc/{env}\n")
	var out bytes.Buffer
	got := askInit(in, &out, initPlan{Targets: []string{"github"}, Environments: []string{"production"}, Repo: "acme/api", VaultMount: "secret", VaultPath: "api/{env}"})
	if strings.Join(got.Targets, ",") != "github,vault" || strings.Join(got.Environments, ",") != "production" ||
		got.Repo != "acme/api" || got.VaultAddr != "https://v:8200" || got.VaultMount != "secret" || got.VaultPath != "svc/{env}" {
		t.Fatalf("got %+v\nprompts:\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "Repository (owner/name or group/project) [acme/api]") {
		t.Fatalf("prompts:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Environments") {
		t.Fatalf("environments must come from the branch, not a prompt:\n%s", out.String())
	}
}

func TestEnvForBranch(t *testing.T) {
	for branch, want := range map[string]string{
		"main": "production", "master": "production", "develop": "staging", "dev": "staging",
		"qa": "qa", "Release/1.2": "release-1-2", "feature/login_v2": "feature-login_v2", "--": "production",
	} {
		if got := envForBranch(branch); got != want {
			t.Errorf("envForBranch(%q) = %q, want %q", branch, got, want)
		}
	}
}

func TestInitWizardEnvironmentFromBranch(t *testing.T) {
	for _, tc := range []struct {
		git          fakeGit
		flagEnvs     string
		wantBranches map[string]string
	}{
		{git: fakeGit{remote: ghRemote, branch: "master"}, wantBranches: map[string]string{"master": "production"}},
		{git: fakeGit{remote: ghRemote, branch: "feature/login"}, wantBranches: map[string]string{"feature/login": "feature-login"}},
		{git: fakeGit{remote: ghRemote, branchErr: errors.New("detached")}, wantBranches: map[string]string{"main": "production"}},
		{git: fakeGit{remote: ghRemote, branch: "master"}, flagEnvs: "production,staging", wantBranches: map[string]string{"master": "production", "develop": "staging"}},
	} {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "env4ci.yaml")
		var out bytes.Buffer
		if err := cmdInitWizard(opts{config: cfg, envs: tc.flagEnvs}, strings.NewReader("\n\n"), &out, tc.git, true); err != nil {
			t.Fatal(err)
		}
		c, err := config.Load(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c.Branches, tc.wantBranches) {
			t.Errorf("branch %q: branches = %v, want %v\n%s", tc.git.branch, c.Branches, tc.wantBranches, out.String())
		}
		for _, env := range tc.wantBranches {
			if _, err := os.Stat(filepath.Join(dir, ".github", env+".env")); err != nil {
				t.Errorf("branch %q: environment file: %v", tc.git.branch, err)
			}
		}
		if strings.Contains(out.String(), "Environments (") {
			t.Errorf("asked for environments:\n%s", out.String())
		}
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

func TestInitWizardEnvironmentsFromCI(t *testing.T) {
	for name, tc := range map[string]struct {
		workflow     string
		wantBranches map[string]string
	}{
		"every branch: current branch and guesses": {
			workflow: "on: push\njobs:\n" +
				"  prod:\n    runs-on: ubuntu-latest\n    environment: production\n    steps:\n      - run: echo ${{ secrets.DB_PASSWORD }}\n" +
				"  stage:\n    runs-on: ubuntu-latest\n    environment: staging\n    steps:\n      - run: echo ${{ vars.APP_URL }}\n",
			wantBranches: map[string]string{"master": "production", "develop": "staging"},
		},
		"one branch in CI wins": {
			workflow: "on:\n  push:\n    branches: [release]\njobs:\n" +
				"  qa:\n    runs-on: ubuntu-latest\n    environment: qa\n    steps:\n      - run: echo ${{ secrets.DB_PASSWORD }}\n",
			wantBranches: map[string]string{"release": "qa"},
		},
		"unusable name falls back to the branch": {
			workflow: "on: push\njobs:\n" +
				"  x:\n    runs-on: ubuntu-latest\n    environment: ${{ inputs.target }}\n    steps:\n      - run: echo ${{ secrets.DB_PASSWORD }}\n",
			wantBranches: map[string]string{"master": "production"},
		},
	} {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755)
		os.WriteFile(filepath.Join(dir, ".github", "workflows", "deploy.yml"), []byte(tc.workflow), 0o644)
		cfg := filepath.Join(dir, "env4ci.yaml")
		var out bytes.Buffer
		if err := cmdInitWizard(opts{config: cfg}, strings.NewReader("\n\n"), &out, fakeGit{remote: ghRemote, branch: "master"}, true); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c, err := config.Load(cfg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(c.Branches, tc.wantBranches) {
			t.Errorf("%s: branches = %v, want %v\n%s", name, c.Branches, tc.wantBranches, out.String())
		}
		for _, env := range tc.wantBranches {
			b, err := os.ReadFile(filepath.Join(dir, ".github", env+".env"))
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			// The environment's name is the file's first key.
			for _, line := range strings.Split(string(b), "\n") {
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if line != "ENVIRONMENT="+env {
					t.Errorf("%s: first key of %s.env = %q\n%s", name, env, line, b)
				}
				break
			}
			if strings.Count(string(b), "ENVIRONMENT=") != 1 {
				t.Errorf("%s: ENVIRONMENT written more than once:\n%s", name, b)
			}
		}
	}
}

func TestInitWizardCIEdgeCases(t *testing.T) {
	workflow := func(body string) string {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755)
		os.WriteFile(filepath.Join(dir, ".github", "workflows", "deploy.yml"), []byte(body), 0o644)
		return dir
	}
	job := func(id, env string) string {
		return "  " + id + ":\n    runs-on: ubuntu-latest\n    environment: " + env + "\n    steps:\n      - run: echo ${{ secrets.DB_PASSWORD }}\n"
	}
	noBranch := fakeGit{remote: ghRemote, branchErr: errors.New("detached")}

	// Two environments guessing the same branch: the second keeps its own name.
	dir := workflow("on: push\njobs:\n" + job("a", "prod") + job("b", "production"))
	cfg := filepath.Join(dir, "env4ci.yaml")
	if err := cmdInitWizard(opts{config: cfg}, strings.NewReader("\n\n"), io.Discard, noBranch, true); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"main": "prod", "production": "production"}; !reflect.DeepEqual(c.Branches, want) {
		t.Errorf("branches = %v, want %v", c.Branches, want)
	}

	// Unreadable CI file: fail before env4ci.yaml is written.
	dir = workflow("jobs: [unclosed\n")
	cfg = filepath.Join(dir, "env4ci.yaml")
	if err := cmdInitWizard(opts{config: cfg}, strings.NewReader("\n\n"), io.Discard, noBranch, true); err == nil {
		t.Fatal("want CI scan error")
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Fatal("env4ci.yaml written despite the CI scan error")
	}
}

func TestInitJenkins(t *testing.T) {
	t.Setenv("JENKINS_URL", "")
	var out bytes.Buffer
	got := askInit(strings.NewReader("jenkins\nhttps://ci.example\n"), &out, initPlan{Environments: []string{"production"}})
	if got.JenkinsURL != "https://ci.example" || strings.Join(got.Targets, ",") != "jenkins" {
		t.Fatalf("got %+v\n%s", got, out.String())
	}
	b, err := renderInit(got)
	if err != nil || !strings.Contains(string(b), "url: https://ci.example") {
		t.Fatalf("%v\n%s", err, b)
	}
	if c := got.config(); c.Targets.Jenkins == nil || c.Targets.Jenkins.URL != "https://ci.example" {
		t.Fatalf("%+v", c.Targets)
	}
}
