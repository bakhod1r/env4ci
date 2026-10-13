package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/infrastructure/selfupdate"
)

type fakeReleases struct {
	tag         string
	latestErr   error
	bin         []byte
	downloadErr error
	asked       string // "tag goos/goarch" of the last Download
}

func (f *fakeReleases) Latest(context.Context) (string, error) { return f.tag, f.latestErr }

func (f *fakeReleases) Download(_ context.Context, tag, goos, goarch string) ([]byte, error) {
	f.asked = tag + " " + goos + "/" + goarch
	return f.bin, f.downloadErr
}

// installed pretends the running binary is version v at a temp path.
func installed(t *testing.T, v string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env4ci")
	os.WriteFile(path, []byte("old"), 0o755)
	oldVersion, oldExe := version, executable
	version, executable = v, func() (string, error) { return path, nil }
	t.Cleanup(func() { version, executable = oldVersion, oldExe })
	return path
}

func TestUpdateInstallsNewerRelease(t *testing.T) {
	path := installed(t, "0.3.0")
	src := &fakeReleases{tag: "v0.3.1", bin: []byte("new")}
	var out bytes.Buffer
	if err := cmdUpdate(context.Background(), opts{}, &out, src, "linux", "arm64"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Fatalf("binary = %q\n%s", b, out.String())
	}
	if src.asked != "v0.3.1 linux/arm64" {
		t.Fatalf("asked %q", src.asked)
	}
	if !strings.Contains(out.String(), "updated env4ci 0.3.0 -> 0.3.1") {
		t.Fatal(out.String())
	}
}

func TestUpdateLeavesBinaryAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		have, tag string
		check     bool
		want      string
	}{
		"already latest":     {have: "0.3.1", tag: "v0.3.1", want: "0.3.1 is the latest version"},
		"ahead of release":   {have: "v0.4.0-2-gabc", tag: "v0.3.1", want: "0.4.0 is the latest version"},
		"--check only tells": {have: "0.3.0", tag: "v0.3.1", check: true, want: "0.3.1 is available (you have 0.3.0); run: env4ci update"},
	} {
		path := installed(t, tc.have)
		src := &fakeReleases{tag: tc.tag, bin: []byte("new")}
		var out bytes.Buffer
		if err := cmdUpdate(context.Background(), opts{check: tc.check}, &out, src, "linux", "amd64"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b, _ := os.ReadFile(path); string(b) != "old" || src.asked != "" {
			t.Errorf("%s: binary = %q, downloaded %q", name, b, src.asked)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s: %s", name, out.String())
		}
	}
}

func TestUpdatePackageManaged(t *testing.T) {
	installed(t, "0.3.0")
	for path, want := range map[string]string{
		"/opt/homebrew/Caskroom/env4ci/0.3.0/env4ci":       "brew upgrade env4ci",
		"/usr/local/Cellar/env4ci/0.3.0/bin/env4ci":        "brew upgrade env4ci",
		"/home/linuxbrew/.linuxbrew/Homebrew/x/env4ci":     "brew upgrade env4ci",
		`C:\Users\me\scoop\apps\env4ci\current\env4ci.exe`: "scoop update env4ci",
	} {
		executable = func() (string, error) { return path, nil }
		src := &fakeReleases{tag: "v0.3.1"}
		var out bytes.Buffer
		if err := cmdUpdate(context.Background(), opts{}, &out, src, "linux", "amd64"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "run: "+want) || src.asked != "" {
			t.Errorf("%s: %s (downloaded %q)", path, out.String(), src.asked)
		}
	}
	if got := packageManagerCommand("/usr/local/bin/env4ci"); got != "" {
		t.Errorf("plain install: %q", got)
	}
}

func TestUpdateErrors(t *testing.T) {
	ctx := context.Background()
	fail := func(want string, src *fakeReleases) {
		t.Helper()
		var out bytes.Buffer
		err := cmdUpdate(ctx, opts{}, &out, src, "linux", "amd64")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q\n%s", err, want, out.String())
		}
	}

	installed(t, "dev")
	fail("development build (dev)", &fakeReleases{tag: "v0.3.1"})

	path := installed(t, "0.3.0")
	fail("offline", &fakeReleases{latestErr: errors.New("offline")})
	fail("latest release", &fakeReleases{tag: "vnext"})
	fail("checksum mismatch", &fakeReleases{tag: "v0.3.1", downloadErr: errors.New("checksum mismatch")})
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Fatalf("binary touched: %q", b)
	}

	replaceBinary = func(string, []byte) error { return errors.New("permission denied") }
	fail("permission denied (run with more rights, or reinstall)", &fakeReleases{tag: "v0.3.1"})
	replaceBinary = selfupdate.Replace

	executable = func() (string, error) { return "", errors.New("no path") }
	fail("no path", &fakeReleases{tag: "v0.3.1"})
}

func TestRunUpdate(t *testing.T) {
	installed(t, "0.3.1")
	old := newReleaseSource
	newReleaseSource = func() releaseSource { return &fakeReleases{tag: "v0.3.1"} }
	defer func() { newReleaseSource = old }()
	if s := mustRun(t, "update", "--check"); !strings.Contains(s, "is the latest version") {
		t.Fatal(s)
	}
	// The real source is a GitHub release reader.
	if _, ok := old().(selfupdate.Source); !ok {
		t.Fatalf("%T", old())
	}
}
