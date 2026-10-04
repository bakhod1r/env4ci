package gitinfo

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestParseRemoteBadURLAndUnknownBaseURL(t *testing.T) {
	if _, err := ParseRemote("https://bad host/%zz/x"); err == nil || !strings.Contains(err.Error(), "parse remote") {
		t.Fatalf("err = %v", err)
	}
	if got := (Remote{Host: "git.corp.io"}).BaseURL(); got != "" {
		t.Fatalf("got %q", got)
	}
}

func clearCI(t *testing.T) {
	for _, k := range ciBranchVars {
		t.Setenv(k, "")
	}
}

func TestCurrentBranchFromCI(t *testing.T) {
	clearCI(t)
	t.Setenv("CI_COMMIT_BRANCH", "release/1")
	if b, err := CurrentBranch(t.TempDir()); err != nil || b != "release/1" {
		t.Fatalf("b=%q err=%v", b, err)
	}
}

func TestCurrentBranchErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	clearCI(t)
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", dir+"/..")
	if _, err := CurrentBranch(dir); err == nil {
		t.Fatal("not a repo: want error")
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "x")
	run("checkout", "-q", "--detach")
	if _, err := CurrentBranch(dir); err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("err = %v", err)
	}
}

func TestGitBinaryMissing(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := ReadRemote(t.TempDir()); err == nil || !strings.Contains(err.Error(), "git remote") {
		t.Fatalf("err = %v", err)
	}
	if IsIgnored(t.TempDir(), "x") {
		t.Fatal("IsIgnored without git must be false")
	}
}
