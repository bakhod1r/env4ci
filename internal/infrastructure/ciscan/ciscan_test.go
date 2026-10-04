package ciscan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

const workflow = `
name: deploy
on: push
env:
  REGION: ${{ vars.AWS_REGION }}
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo ${{ secrets.GITHUB_TOKEN }} ${{ secrets.CODECOV_TOKEN }}
  deploy:
    environment: production
    runs-on: ubuntu-latest
    env:
      DATABASE_URL: ${{ secrets.DATABASE_URL }}
    steps:
      - run: deploy --port ${{ vars.APP_PORT }} --key ${{secrets.JWT_SECRET}}
  stage:
    environment:
      name: staging
      url: https://x
    steps:
      - run: echo ${{ secrets.DATABASE_URL }}
`

const gitlabCI = `
variables:
  GO_VERSION: "1.25"
stages: [test, deploy]
test:
  script:
    - go test ./... && echo $GO_VERSION $CI_COMMIT_SHA $HOME
    - curl -H "token: $SONAR_TOKEN" ${SONAR_HOST}
deploy:
  environment: production
  variables:
    TARGET: prod
  script:
    - deploy --db "$DATABASE_URL" --to $TARGET --log $LOG_LEVEL
`

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func index(refs []domain.Reference) map[string]domain.Reference {
	m := map[string]domain.Reference{}
	for _, r := range refs {
		m[r.Environment+"/"+r.Key] = r
	}
	return m
}

func TestScanGitHub(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".github/workflows/deploy.yml", workflow)
	refs, err := Scan(root, domain.DefaultClassifier())
	if err != nil {
		t.Fatal(err)
	}
	m := index(refs)
	want := map[string]domain.Kind{
		"/AWS_REGION":             domain.KindVariable,
		"/CODECOV_TOKEN":          domain.KindSecret,
		"production/DATABASE_URL": domain.KindSecret,
		"production/APP_PORT":     domain.KindVariable,
		"production/JWT_SECRET":   domain.KindSecret,
		"staging/DATABASE_URL":    domain.KindSecret,
	}
	if len(m) != len(want) {
		t.Fatalf("got %d refs: %+v", len(m), refs)
	}
	for k, kind := range want {
		r, ok := m[k]
		if !ok || r.Kind != kind || r.Provider != "github" {
			t.Errorf("%s: got %+v", k, r)
		}
	}
	if m["/AWS_REGION"].Sources[0] != ".github/workflows/deploy.yml" {
		t.Errorf("source = %v", m["/AWS_REGION"].Sources)
	}
}

func TestScanGitLab(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".gitlab-ci.yml", gitlabCI)
	refs, err := Scan(root, domain.DefaultClassifier())
	if err != nil {
		t.Fatal(err)
	}
	m := index(refs)
	want := map[string]domain.Kind{
		"/SONAR_TOKEN":            domain.KindSecret,
		"/SONAR_HOST":             domain.KindSecret, // unknown => secret
		"production/DATABASE_URL": domain.KindSecret,
		"production/LOG_LEVEL":    domain.KindVariable,
	}
	if len(m) != len(want) {
		t.Fatalf("got %d refs: %+v", len(m), refs)
	}
	for k, kind := range want {
		if r, ok := m[k]; !ok || r.Kind != kind || r.Provider != "gitlab" {
			t.Errorf("%s: got %+v", k, r)
		}
	}
}

func TestScanNothing(t *testing.T) {
	refs, err := Scan(t.TempDir(), domain.DefaultClassifier())
	if err != nil || len(refs) != 0 {
		t.Fatalf("refs=%v err=%v", refs, err)
	}
}
