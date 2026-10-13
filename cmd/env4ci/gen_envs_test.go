package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
)

// ciJob is one GitHub job deploying to env and reading one secret.
func ciJob(id, env string) string {
	return "  " + id + ":\n    runs-on: ubuntu-latest\n    environment: " + env + "\n    steps:\n      - run: echo ${{ secrets.DB_PASSWORD }}\n"
}

// ciProject writes env4ci.yaml and one workflow into a temp project.
func ciProject(t *testing.T, cfg, workflow string) (dir, cfgPath string) {
	t.Helper()
	dir = t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755)
	os.WriteFile(filepath.Join(dir, ".github", "workflows", "deploy.yml"), []byte(workflow), 0o644)
	cfgPath = filepath.Join(dir, "env4ci.yaml")
	os.WriteFile(cfgPath, []byte(cfg), 0o644)
	return dir, cfgPath
}

func gen(t *testing.T, dir, cfgPath string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"gen", "-c", cfgPath, "--dir", dir}, nil, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String()
}

func TestGenAddsEnvironmentsCIGained(t *testing.T) {
	initial, err := renderInit(initPlan{Targets: []string{"github"}, Environments: []string{"production"}, Repo: "acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	dir, cfgPath := ciProject(t, string(initial), "on: push\njobs:\n"+ciJob("a", "production")+ciJob("b", "staging")+ciJob("c", "${{ inputs.target }}"))

	out := gen(t, dir, cfgPath)
	if !strings.Contains(out, "added environment staging to") || !strings.Contains(out, "branch develop, file .github/staging.env") {
		t.Fatal(out)
	}
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"production": ".github/production.env", "staging": ".github/staging.env"}; !reflect.DeepEqual(c.Environments, want) {
		t.Errorf("environments = %v", c.Environments)
	}
	if want := map[string]string{"main": "production", "develop": "staging"}; !reflect.DeepEqual(c.Branches, want) {
		t.Errorf("branches = %v", c.Branches)
	}
	text, _ := os.ReadFile(cfgPath)
	for _, kept := range []string{"# Branch -> environment.", "# Environment -> local file.", "  github:\n    repo: acme/api"} {
		if !strings.Contains(string(text), kept) {
			t.Errorf("config lost %q:\n%s", kept, text)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".github", "staging.env")); err != nil || !strings.Contains(string(b), "ENVIRONMENT=staging\n") || !strings.Contains(string(b), "DB_PASSWORD=") {
		t.Errorf("staging.env: %v\n%s", err, b)
	}

	// Nothing new the second time.
	if out := gen(t, dir, cfgPath); strings.Contains(out, "added environment") {
		t.Fatal(out)
	}
	again, _ := os.ReadFile(cfgPath)
	if !bytes.Equal(text, again) {
		t.Fatalf("config rewritten:\n%s", again)
	}
}

func TestGenNewEnvironmentBranchAndFile(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg, workflow string
		wantEnvs      map[string]string
		wantBranches  map[string]string
	}{
		"CI names the branch": {
			cfg:          "branches:\n    \"main\": production\nenvironments:\n    production: .env.production\ntargets:\n  github: { repo: a/b }\n",
			workflow:     "on:\n  push:\n    branches: [release]\njobs:\n" + ciJob("q", "qa"),
			wantEnvs:     map[string]string{"production": ".env.production", "qa": ".env.qa"},
			wantBranches: map[string]string{"main": "production", "release": "qa"},
		},
		"guessed branch is taken: the environment's own name": {
			cfg:          "branches:\n  \"develop\": production\nenvironments:\n  production: ci/production.env\ntargets:\n  github: { repo: a/b }\n",
			workflow:     "on: push\njobs:\n" + ciJob("s", "staging"),
			wantEnvs:     map[string]string{"production": "ci/production.env", "staging": "ci/staging.env"},
			wantBranches: map[string]string{"develop": "production", "staging": "staging"},
		},
		"every candidate branch is taken: no mapping": {
			cfg:          "branches:\n  \"develop\": production\n  \"staging\": production\nenvironments:\n  production: secrets/prod.txt\ntargets:\n  github: { repo: a/b }\n",
			workflow:     "on: push\njobs:\n" + ciJob("s", "staging"),
			wantEnvs:     map[string]string{"production": "secrets/prod.txt", "staging": "secrets/staging.env"},
			wantBranches: map[string]string{"develop": "production", "staging": "production"},
		},
		"no branches: in the config, root-level odd file name": {
			cfg:          "# mine\r\nenvironments: # files\r\n\r\n  # live\r\n  production: prodfile\r\ntargets:\r\n  github: { repo: a/b }\r\n",
			workflow:     "on: push\njobs:\n" + ciJob("s", "staging"),
			wantEnvs:     map[string]string{"production": "prodfile", "staging": ".env.staging"},
			wantBranches: nil,
		},
	} {
		dir, cfgPath := ciProject(t, tc.cfg, tc.workflow)
		out := gen(t, dir, cfgPath)
		c, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if !reflect.DeepEqual(c.Environments, tc.wantEnvs) || !reflect.DeepEqual(c.Branches, tc.wantBranches) {
			text, _ := os.ReadFile(cfgPath)
			t.Errorf("%s: environments = %v, branches = %v\n%s\n%s", name, c.Environments, c.Branches, out, text)
		}
		for env, file := range tc.wantEnvs {
			if env != "production" {
				if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
					t.Errorf("%s: %v", name, err)
				}
			}
		}
		if text, _ := os.ReadFile(cfgPath); strings.Contains(tc.cfg, "\r\n") && strings.Contains(strings.ReplaceAll(string(text), "\r\n", ""), "\n") {
			t.Errorf("%s: line endings changed:\n%q", name, text)
		}
	}
}

func TestGenLeavesConfigItCannotEdit(t *testing.T) {
	workflow := "on: push\njobs:\n" + ciJob("a", "production") + ciJob("b", "staging")
	for name, cfg := range map[string]string{
		"environments in flow style": "environments: { production: .env.production }\ntargets:\n  github: { repo: a/b }\n",
		"branches in flow style":     "branches: { main: production }\nenvironments:\n  production: .env.production\ntargets:\n  github: { repo: a/b }\n",
	} {
		dir, cfgPath := ciProject(t, cfg, workflow)
		out := gen(t, dir, cfgPath)
		if !strings.Contains(out, "CI deploys to staging") || !strings.Contains(out, "by hand") {
			t.Errorf("%s:\n%s", name, out)
		}
		if text, _ := os.ReadFile(cfgPath); string(text) != cfg {
			t.Errorf("%s: config changed:\n%s", name, text)
		}
		if _, err := os.Stat(filepath.Join(dir, ".env.staging")); err == nil {
			t.Errorf("%s: .env.staging created without a config entry", name)
		}
	}

	// One-file mode (no environments:) stays one-file.
	dir, cfgPath := ciProject(t, "targets:\n  github: { repo: a/b }\n", workflow)
	if out := gen(t, dir, cfgPath); strings.Contains(out, "staging") && strings.Contains(out, "added environment") {
		t.Fatal(out)
	}
}

func TestGenConfigWriteFailures(t *testing.T) {
	cfg := "environments:\n  production: .env.production\ntargets:\n  github: { repo: a/b }\n"
	workflow := "on: push\njobs:\n" + ciJob("b", "staging")
	loaded := func(cfgPath string) config.Config {
		c, err := config.Load(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	defer func() { openFile = os.OpenFile }()

	// Config cannot be opened for writing.
	dir, cfgPath := ciProject(t, cfg, workflow)
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if name == cfgPath {
			return nil, os.ErrPermission
		}
		return os.OpenFile(name, flag, perm)
	}
	if err := cmdGen(loaded(cfgPath), opts{config: cfgPath, dir: dir}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("want open error")
	}
	// Opened, but the write fails.
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if name == cfgPath {
			return os.Open(name) // read-only
		}
		return os.OpenFile(name, flag, perm)
	}
	if err := cmdGen(loaded(cfgPath), opts{config: cfgPath, dir: dir}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("want write error")
	}
	openFile = os.OpenFile
	// Config vanished after it was loaded.
	c := loaded(cfgPath)
	os.Remove(cfgPath)
	if err := cmdGen(c, opts{config: cfgPath, dir: dir}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("want read error")
	}
}

func TestInitEnvsFlagIsNotExtendedFromCI(t *testing.T) {
	dir, cfgPath := ciProject(t, "", "on: push\njobs:\n"+ciJob("a", "production")+ciJob("b", "staging"))
	os.Remove(cfgPath)
	var out bytes.Buffer
	if err := cmdInitWizard(opts{config: cfgPath, envs: "production"}, nil, &out, fakeGit{remote: ghRemote}, false); err != nil {
		t.Fatal(err)
	}
	c, _ := config.Load(cfgPath)
	if len(c.Environments) != 1 || c.Environments["production"] == "" {
		t.Fatalf("environments = %v\n%s", c.Environments, out.String())
	}
	_ = dir
}

// branchJob reads one secret and names no environment (env "") or deploys to env.
func branchWorkflow(branches, env string) string {
	w := "on:\n  push:\n    branches: " + branches + "\njobs:\n  deploy:\n    runs-on: ubuntu-latest\n"
	if env != "" {
		w += "    environment: " + env + "\n"
	}
	return w + "    steps:\n      - run: echo ${{ secrets.DB_PASSWORD }}\n"
}

func TestInitEnvironmentsFromCIBranchList(t *testing.T) {
	for name, tc := range map[string]struct {
		workflow     string
		wantBranches map[string]string
	}{
		"branches: [dev, main], no environment": {
			workflow:     branchWorkflow("[dev, main]", ""),
			wantBranches: map[string]string{"dev": "staging", "main": "production"},
		},
		"two branches, one environment": {
			workflow:     branchWorkflow("[main, master, qa]", ""),
			wantBranches: map[string]string{"main": "production", "qa": "qa"},
		},
		"environment is the branch's name": {
			workflow:     branchWorkflow(`[dev, main, "--"]`, "${{ github.ref_name }}"),
			wantBranches: map[string]string{"dev": "dev", "main": "main"},
		},
		"patterns are not branches: the checked-out branch decides": {
			workflow:     branchWorkflow(`["release/*"]`, ""),
			wantBranches: map[string]string{"master": "production"},
		},
	} {
		dir, cfgPath := ciProject(t, "", tc.workflow)
		os.Remove(cfgPath)
		var out bytes.Buffer
		if err := cmdInitWizard(opts{config: cfgPath}, strings.NewReader("\n\n"), &out, fakeGit{remote: ghRemote, branch: "master"}, true); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c, err := config.Load(cfgPath)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(c.Branches, tc.wantBranches) {
			t.Errorf("%s: branches = %v, want %v\n%s", name, c.Branches, tc.wantBranches, out.String())
		}
		for _, env := range tc.wantBranches {
			if b, err := os.ReadFile(filepath.Join(dir, ".github", env+".env")); err != nil || !strings.Contains(string(b), "DB_PASSWORD=") {
				t.Errorf("%s: %s.env: %v\n%s", name, env, err, b)
			}
		}
	}
}

func TestGenEnvironmentsFromCIBranchList(t *testing.T) {
	// CI gained a branch: its environment is added under that branch.
	cfg := "branches:\n  \"main\": production\nenvironments:\n  production: .github/production.env\ntargets:\n  github: { repo: a/b }\n"
	dir, cfgPath := ciProject(t, cfg, branchWorkflow("[dev, main]", ""))
	out := gen(t, dir, cfgPath)
	c, _ := config.Load(cfgPath)
	if want := map[string]string{"main": "production", "dev": "staging"}; !reflect.DeepEqual(c.Branches, want) {
		t.Fatalf("branches = %v\n%s", c.Branches, out)
	}
	if c.Environments["staging"] != ".github/staging.env" {
		t.Fatalf("environments = %v", c.Environments)
	}

	// The branch already deploys to an environment the user named: nothing is added.
	cfg = "branches:\n  \"main\": live\nenvironments:\n  live: .github/live.env\ntargets:\n  github: { repo: a/b }\n"
	dir, cfgPath = ciProject(t, cfg, branchWorkflow("[main]", ""))
	if out := gen(t, dir, cfgPath); strings.Contains(out, "added environment") {
		t.Fatal(out)
	}
	if text, _ := os.ReadFile(cfgPath); string(text) != cfg {
		t.Fatalf("config changed:\n%s", text)
	}
}

// droppedProject: env4ci.yaml knows production (main), staging (dev) and
// prod2 (live); CI now runs on main and live only, so staging is gone.
func droppedProject(t *testing.T) (dir, cfgPath string) {
	t.Helper()
	cfg := "# mine\nbranches:\n  \"main\": production\n  \"dev\": staging # old\n  \"live\": prod2\nenvironments:\n  production: .github/production.env\n  staging: .github/staging.env\n  prod2: .github/prod2.env\ntargets:\n  github: { repo: a/b }\n"
	dir, cfgPath = ciProject(t, cfg, branchWorkflow("[main, live]", ""))
	os.WriteFile(filepath.Join(dir, ".github", "staging.env"), []byte("ENVIRONMENT=staging\nDB_PASSWORD=keep\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin\n/.github/staging.env\n"), 0o644)
	return dir, cfgPath
}

func genWith(t *testing.T, cfgPath, dir string, o opts, in string) string {
	t.Helper()
	c, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	o.config, o.dir = cfgPath, dir
	var out bytes.Buffer
	if err := cmdGen(c, o, strings.NewReader(in), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String()
}

func TestGenRemovesEnvironmentsCIDropped(t *testing.T) {
	for name, o := range map[string]opts{"-y": {yes: true}, "terminal y": {interactive: true}} {
		dir, cfgPath := droppedProject(t)
		out := genWith(t, cfgPath, dir, o, "y\n")
		if !strings.Contains(out, "CI no longer deploys to environment staging (branch dev)") || !strings.Contains(out, "removed environment staging") {
			t.Fatalf("%s:\n%s", name, out)
		}
		if o.interactive && !strings.Contains(out, "delete .github/staging.env, which has 1 filled values? [y/N]") {
			t.Errorf("%s: prompt:\n%s", name, out)
		}
		text, _ := os.ReadFile(cfgPath)
		want := "# mine\nbranches:\n  \"main\": production\n  \"live\": prod2\nenvironments:\n  production: .github/production.env\n  prod2: .github/prod2.env\ntargets:\n  github: { repo: a/b }\n"
		if string(text) != want {
			t.Errorf("%s: config:\n%s", name, text)
		}
		if _, err := os.Stat(filepath.Join(dir, ".github", "staging.env")); err == nil {
			t.Errorf("%s: staging.env still there", name)
		}
		if gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); strings.Contains(string(gi), "staging") || !strings.HasPrefix(string(gi), "bin\n") {
			t.Errorf("%s: .gitignore:\n%s", name, gi)
		}
	}
}

func TestGenKeepsDroppedEnvironmentWithoutYes(t *testing.T) {
	for name, tc := range map[string]struct {
		o  opts
		in string
	}{"not a terminal": {opts{}, ""}, "terminal n": {opts{interactive: true}, "n\n"}} {
		dir, cfgPath := droppedProject(t)
		before, _ := os.ReadFile(cfgPath)
		out := genWith(t, cfgPath, dir, tc.o, tc.in)
		if !strings.Contains(out, "kept; to remove it run env4ci gen -y") {
			t.Errorf("%s:\n%s", name, out)
		}
		if after, _ := os.ReadFile(cfgPath); !bytes.Equal(before, after) {
			t.Errorf("%s: config changed", name)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, ".github", "staging.env")); !strings.Contains(string(b), "DB_PASSWORD=keep") {
			t.Errorf("%s: staging.env changed:\n%s", name, b)
		}
	}
	// gen alone (nil stdin), and CI naming no environment: nothing to compare.
	dir, cfgPath := ciProject(t, "environments:\n  production: .env.production\ntargets:\n  github: { repo: a/b }\n",
		"on: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: echo ${{ secrets.A }}\n")
	if out := gen(t, dir, cfgPath); strings.Contains(out, "no longer") {
		t.Fatal(out)
	}
	c, _ := config.Load(cfgPath)
	if err := cmdGen(c, opts{config: cfgPath, dir: dir, yes: true}, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func TestGenRemoveEdgeCases(t *testing.T) {
	// No branches: and no file on disk, flow-style config left to the user.
	dir, cfgPath := ciProject(t, "environments:\n  production: .env.production\n  old: .env.old\ntargets:\n  github: { repo: a/b }\n", "on: push\njobs:\n"+ciJob("a", "production"))
	if out := genWith(t, cfgPath, dir, opts{yes: true}, ""); !strings.Contains(out, "removed environment old") {
		t.Fatal(out)
	}
	if c, _ := config.Load(cfgPath); c.Environments["old"] != "" {
		t.Fatal("old still in config")
	}
	dir, cfgPath = ciProject(t, "environments: { production: .env.production, old: .env.old }\ntargets:\n  github: { repo: a/b }\n", "on: push\njobs:\n"+ciJob("a", "production"))
	if out := genWith(t, cfgPath, dir, opts{yes: true}, ""); !strings.Contains(out, "remove old from it by hand") {
		t.Fatal(out)
	}

	defer func() { openFile = os.OpenFile }()
	fail := func(setup func(dir, cfgPath string)) {
		t.Helper()
		dir, cfgPath := droppedProject(t)
		c, _ := config.Load(cfgPath)
		setup(dir, cfgPath)
		if err := cmdGen(c, opts{config: cfgPath, dir: dir, yes: true}, nil, &bytes.Buffer{}); err == nil {
			t.Fatal("want error")
		}
		openFile = os.OpenFile
	}
	failOpen := func(target string) func(string, string) {
		return func(dir, cfgPath string) {
			path := filepath.Join(dir, target)
			if target == "" {
				path = cfgPath
			}
			openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
				if name == path && flag&os.O_TRUNC != 0 {
					return nil, os.ErrPermission
				}
				return os.OpenFile(name, flag, perm)
			}
		}
	}
	fail(failOpen(""))           // config cannot be rewritten
	fail(failOpen(".gitignore")) // .gitignore cannot be rewritten
	fail(func(dir, _ string) {   // .gitignore unreadable
		os.Remove(filepath.Join(dir, ".gitignore"))
		os.Mkdir(filepath.Join(dir, ".gitignore"), 0o755)
	})
	fail(func(dir, _ string) { // env file is a non-empty folder
		f := filepath.Join(dir, ".github", "staging.env")
		os.Remove(f)
		os.MkdirAll(filepath.Join(f, "x"), 0o755)
	})
	fail(func(_, cfgPath string) { os.Remove(cfgPath) }) // config vanished

	fail(func(_, cfgPath string) { // config opens, but the write fails
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			if name == cfgPath {
				return os.Open(name)
			}
			return os.OpenFile(name, flag, perm)
		}
	})

	// CRLF config, .gitignore without the line: both kept as they are.
	dir, cfgPath = ciProject(t, "environments:\r\n  production: .env.production\r\n  old: .env.old\r\ntargets:\r\n  github: { repo: a/b }\r\n", "on: push\njobs:\n"+ciJob("a", "production"))
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin\n"), 0o644)
	genWith(t, cfgPath, dir, opts{yes: true}, "")
	if text, _ := os.ReadFile(cfgPath); string(text) != "environments:\r\n  production: .env.production\r\ntargets:\r\n  github: { repo: a/b }\r\n" {
		t.Fatalf("%q", text)
	}

	if got := filledValues(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Fatal(got)
	}
	if err := gitignoreRemove(filepath.Join(t.TempDir(), "none"), "x"); err != nil {
		t.Fatal(err)
	}
}
