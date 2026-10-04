package ciscan

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

var update = flag.Bool("update", false, "rewrite golden files")

// render prints references one per line, stable and diff-friendly.
func render(refs []domain.Reference) string {
	var b strings.Builder
	for _, r := range refs {
		env := r.Environment
		if env == "" {
			env = "-"
		}
		fmt.Fprintf(&b, "%-11s %-20s %-8s %-6s stages=%s branches=%s sources=%s\n",
			env, r.Key, r.Kind, r.Provider, strings.Join(r.Stages, ","), strings.Join(r.Branches, ","), strings.Join(r.Sources, ","))
	}
	return b.String()
}

func TestScanFixtures(t *testing.T) {
	for _, name := range []string{"github", "gitlab", "mixed"} {
		t.Run(name, func(t *testing.T) {
			refs, err := Scan(filepath.Join("testdata", name), domain.DefaultClassifier())
			if err != nil {
				t.Fatal(err)
			}
			got := render(refs)
			golden := filepath.Join("testdata", name+".golden")
			if *update {
				os.WriteFile(golden, []byte(got), 0o644)
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run: go test ./internal/infrastructure/ciscan -update)", err)
			}
			if got != string(want) {
				t.Errorf("mismatch for %s\n--- got\n%s--- want\n%s", name, got, want)
			}
		})
	}
}

func TestScanNothing(t *testing.T) {
	refs, err := Scan(t.TempDir(), domain.DefaultClassifier())
	if err != nil || len(refs) != 0 {
		t.Fatalf("refs=%v err=%v", refs, err)
	}
}

func TestScanMissingIncludeIsError(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte("include: [{local: nope.yml}]\n"), 0o644)
	if _, err := Scan(dir, domain.DefaultClassifier()); err == nil || !strings.Contains(err.Error(), "nope.yml") {
		t.Fatalf("err = %v", err)
	}
}

func TestScanIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte("include: a.yml\njob: {script: [echo $X_TOKEN]}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.yml"), []byte("include: .gitlab-ci.yml\n"), 0o644)
	refs, err := Scan(dir, domain.DefaultClassifier())
	if err != nil || len(refs) != 1 {
		t.Fatalf("refs=%v err=%v", refs, err)
	}
}

func TestScanInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755)
	os.WriteFile(filepath.Join(dir, ".github", "workflows", "x.yml"), []byte("jobs: [unclosed"), 0o644)
	if _, err := Scan(dir, domain.DefaultClassifier()); err == nil || !strings.Contains(err.Error(), "x.yml") {
		t.Fatalf("err = %v", err)
	}
}
