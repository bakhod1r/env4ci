package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/gitinfo"
)

type fakeGit struct {
	remote    gitinfo.Remote
	remoteErr error
	branch    string
	branchErr error
}

func (f fakeGit) Remote() (gitinfo.Remote, error) { return f.remote, f.remoteErr }
func (f fakeGit) Branch() (string, error)         { return f.branch, f.branchErr }

var (
	ghRemote = gitinfo.Remote{Host: "github.com", Path: "acme/api", Provider: "github"}
	noGit    = fakeGit{remoteErr: errors.New("not a git repository"), branchErr: errors.New("not a git repository")}
	mapCfg   = config.Config{
		Branches:     map[string]string{"main": "production", "develop": "staging", "release/*": "staging"},
		Environments: map[string]string{"production": ".env.production", "staging": ".env.staging"},
	}
)

func TestResolveTarget(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config.Config
		o       opts
		pos     []string
		git     fakeGit
		want    target
		wantErr string
	}{
		{
			name: "everything from git remote and branch map",
			cfg:  mapCfg, git: fakeGit{remote: ghRemote, branch: "main"},
			want: target{Provider: "github", Repo: "acme/api", Environment: "production", File: ".env.production"},
		},
		{
			name: "glob branch",
			cfg:  mapCfg, git: fakeGit{remote: ghRemote, branch: "release/2.0"},
			want: target{Provider: "github", Repo: "acme/api", Environment: "staging", File: ".env.staging"},
		},
		{
			name: "-e beats branch map, -f beats environments",
			cfg:  mapCfg, o: opts{env: "qa", file: "x.env"}, git: fakeGit{remote: ghRemote, branch: "main"},
			want: target{Provider: "github", Repo: "acme/api", Environment: "qa", File: "x.env"},
		},
		{
			name: "--shared ignores branch map",
			cfg:  config.Config{Branches: mapCfg.Branches, Source: ".env.shared"}, o: opts{shared: true},
			git:  fakeGit{remote: ghRemote, branch: "main"},
			want: target{Provider: "github", Repo: "acme/api", Environment: "", File: ".env.shared"},
		},
		{
			name: "unmapped branch is an error, never a silent repo-level push",
			cfg:  mapCfg, git: fakeGit{remote: ghRemote, branch: "feature/x"},
			wantErr: `branch "feature/x" has no environment`,
		},
		{
			name: "detached head with branch map",
			cfg:  mapCfg, git: fakeGit{remote: ghRemote, branchErr: errors.New("detached HEAD")},
			wantErr: "current branch is unknown",
		},
		{
			name: "no branch map: repo level and default file",
			git:  fakeGit{remote: ghRemote, branch: "main"},
			want: target{Provider: "github", Repo: "acme/api", File: ".env"},
		},
		{
			name: "explicit provider differs from remote: remote repo not used",
			pos:  []string{"gitlab"}, git: fakeGit{remote: ghRemote},
			wantErr: "repository unknown",
		},
		{
			name: "explicit provider and config repo, no git",
			cfg:  config.Config{Targets: config.Targets{GitLab: &config.GitLab{Project: "g/p", Environment: "prod", Protected: true}}},
			pos:  []string{"gitlab"}, git: noGit,
			want: target{Provider: "gitlab", Repo: "g/p", Environment: "prod", File: ".env", Protected: true},
		},
		{
			name: "self-hosted gitlab base url from remote",
			git:  fakeGit{remote: gitinfo.Remote{Host: "gitlab.corp.io", Path: "team/app", Provider: "gitlab"}},
			want: target{Provider: "gitlab", Repo: "team/app", BaseURL: "https://gitlab.corp.io", File: ".env"},
		},
		{
			name:    "unknown host and no provider arg",
			git:     fakeGit{remote: gitinfo.Remote{Host: "git.corp.io", Path: "a/b"}},
			wantErr: "provider required",
		},
		{
			name: "bad provider", pos: []string{"bitbucket"}, git: noGit,
			wantErr: "unknown provider",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITLAB_URL", "")
			t.Setenv("CI_SERVER_URL", "")
			got, err := resolveTarget(tc.cfg, tc.o, tc.pos, tc.git)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got.from = nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	tg := target{Provider: "github", Repo: "acme/api", Environment: "production", File: ".env.production", from: []string{"branch main"}}
	if got := tg.Describe(); got != "→ github acme/api · environment production · .env.production  (branch main)" {
		t.Fatalf("got %q", got)
	}
	if got := (target{Provider: "gitlab", Repo: "g/p", File: ".env"}).Describe(); !strings.Contains(got, `scope "*"`) {
		t.Fatalf("got %q", got)
	}
}
