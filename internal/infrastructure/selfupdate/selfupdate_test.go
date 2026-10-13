package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"LICENSE", "README.md", "env4ci"} {
		body, ok := files[name]
		if !ok {
			continue
		}
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"LICENSE", "env4ci.exe"} {
		if body, ok := files[name]; ok {
			w, _ := zw.Create(name)
			w.Write([]byte(body))
		}
	}
	zw.Close()
	return buf.Bytes()
}

func sumLine(name string, b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]) + "  " + name + "\n"
}

// release serves /o/r/releases/latest (redirecting to tag) and the assets.
func release(t *testing.T, tag string, assets map[string][]byte) Source {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/o/r/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/o/r/releases/tag/"+tag, func(w http.ResponseWriter, r *http.Request) {})
	for name, body := range assets {
		mux.HandleFunc("/o/r/releases/download/"+tag+"/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return Source{HTTP: srv.Client(), BaseURL: srv.URL, Repo: "o/r", MaxBytes: 1 << 20}
}

func TestLatest(t *testing.T) {
	ctx := context.Background()
	s := release(t, "v0.3.1", nil)
	if tag, err := s.Latest(ctx); err != nil || tag != "v0.3.1" {
		t.Fatalf("Latest = %q, %v", tag, err)
	}

	// No release yet: the page does not redirect to a tag.
	if _, err := release(t, "none", nil).Latest(ctx); err == nil || !strings.Contains(err.Error(), "latest release of o/r") {
		t.Fatalf("err = %v", err)
	}
	// Not found.
	s.Repo = "o/missing"
	if _, err := s.Latest(ctx); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v", err)
	}
	// Unreachable host and unusable URL.
	s.BaseURL = "http://127.0.0.1:1"
	if _, err := s.Latest(ctx); err == nil {
		t.Fatal("want connection error")
	}
	s.BaseURL = "http://bad\x7fhost"
	if _, err := s.Latest(ctx); err == nil {
		t.Fatal("want URL error")
	}
}

func TestArchiveName(t *testing.T) {
	if got := ArchiveName("v0.3.1", "linux", "arm64"); got != "env4ci_0.3.1_linux_arm64.tar.gz" {
		t.Fatal(got)
	}
	if got := ArchiveName("v0.3.1", "windows", "amd64"); got != "env4ci_0.3.1_windows_amd64.zip" {
		t.Fatal(got)
	}
}

func TestDownload(t *testing.T) {
	ctx := context.Background()
	tgz := tarGz(t, map[string]string{"LICENSE": "MIT", "env4ci": "linux-binary"})
	zp := zipOf(t, map[string]string{"LICENSE": "MIT", "env4ci.exe": "windows-binary"})
	const lin, win = "env4ci_0.3.1_linux_amd64.tar.gz", "env4ci_0.3.1_windows_amd64.zip"
	s := release(t, "v0.3.1", map[string][]byte{
		lin: tgz, win: zp,
		"checksums.txt": []byte(sumLine(lin, tgz) + sumLine(win, zp) + "not a checksum line\n"),
	})

	if b, err := s.Download(ctx, "v0.3.1", "linux", "amd64"); err != nil || string(b) != "linux-binary" {
		t.Fatalf("linux: %q, %v", b, err)
	}
	if b, err := s.Download(ctx, "v0.3.1", "windows", "amd64"); err != nil || string(b) != "windows-binary" {
		t.Fatalf("windows: %q, %v", b, err)
	}
	if _, err := s.Download(ctx, "v0.3.1", "plan9", "amd64"); err == nil || !strings.Contains(err.Error(), "no build for plan9/amd64") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Download(ctx, "v9.9.9", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadRejects(t *testing.T) {
	ctx := context.Background()
	const lin = "env4ci_0.3.1_linux_amd64.tar.gz"
	tgz := tarGz(t, map[string]string{"env4ci": "linux-binary"})

	// Tampered archive.
	s := release(t, "v0.3.1", map[string][]byte{lin: tgz, "checksums.txt": []byte(sumLine(lin, []byte("other")))})
	if _, err := s.Download(ctx, "v0.3.1", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	// Listed in checksums.txt but the asset is missing.
	s = release(t, "v0.3.1", map[string][]byte{"checksums.txt": []byte(sumLine(lin, tgz))})
	if _, err := s.Download(ctx, "v0.3.1", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v", err)
	}
	// Larger than the limit.
	s = release(t, "v0.3.1", map[string][]byte{lin: tgz, "checksums.txt": []byte(sumLine(lin, tgz))})
	s.MaxBytes = 10
	if _, err := s.Download(ctx, "v0.3.1", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "larger than 10 bytes") {
		t.Fatalf("err = %v", err)
	}
	// Unreachable host and unusable URL.
	s.BaseURL = "http://127.0.0.1:1"
	if _, err := s.Download(ctx, "v0.3.1", "linux", "amd64"); err == nil {
		t.Fatal("want connection error")
	}
	s.BaseURL = "http://bad\x7fhost"
	if _, err := s.Download(ctx, "v0.3.1", "linux", "amd64"); err == nil {
		t.Fatal("want URL error")
	}
}

func TestGetBodyCutShort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("short"))
	}))
	defer srv.Close()
	s := Source{HTTP: srv.Client(), MaxBytes: 1 << 20}
	if _, err := s.get(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "read ") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractErrors(t *testing.T) {
	const max = 1 << 20
	gzOf := func(b []byte) []byte {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		w.Write(b)
		w.Close()
		return buf.Bytes()
	}
	// A zip member stored with a compression method Go cannot read.
	var odd bytes.Buffer
	zw := zip.NewWriter(&odd)
	w, _ := zw.CreateRaw(&zip.FileHeader{Name: "env4ci.exe", Method: 99})
	w.Write([]byte("x"))
	zw.Close()

	for name, tc := range map[string]struct {
		file    string
		archive []byte
		want    string
	}{
		"zip: not a zip":         {"a.zip", []byte("garbage"), "a.zip"},
		"zip: binary missing":    {"a.zip", zipOf(t, map[string]string{"LICENSE": "MIT"}), "has no env4ci.exe"},
		"zip: unknown method":    {"a.zip", odd.Bytes(), "a.zip"},
		"tar: not gzip":          {"a.tar.gz", []byte("garbage"), "a.tar.gz"},
		"tar: not a tar":         {"a.tar.gz", gzOf(bytes.Repeat([]byte("garbage!"), 100)), "a.tar.gz"},
		"tar: binary missing":    {"a.tar.gz", tarGz(t, map[string]string{"LICENSE": "MIT"}), "has no env4ci"},
		"tar: binary over limit": {"a.tar.gz", tarGz(t, map[string]string{"env4ci": strings.Repeat("x", max+1)}), "larger than"},
	} {
		bin := "env4ci"
		if strings.HasSuffix(tc.file, ".zip") {
			bin = "env4ci.exe"
		}
		if _, err := extract(tc.file, tc.archive, bin, max); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env4ci")
	os.WriteFile(path, []byte("old"), 0o755)

	if err := Replace(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Fatalf("binary = %q", b)
	}
	if st, _ := os.Stat(path); os.PathSeparator == '/' && st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("not executable: %v", st.Mode())
	}
	for _, leftover := range []string{path + ".new", path + ".old"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("%s left behind", leftover)
		}
	}

	// Folder is missing: nothing can be written.
	if err := Replace(filepath.Join(dir, "missing", "env4ci"), []byte("x")); err == nil {
		t.Fatal("want write error")
	}
	// No binary at path: nothing to move aside, the new file is removed.
	ghost := filepath.Join(dir, "ghost")
	if err := Replace(ghost, []byte("x")); err == nil {
		t.Fatal("want rename error")
	}
	if _, err := os.Stat(ghost + ".new"); err == nil {
		t.Fatal("ghost.new left behind")
	}
}

func TestReplaceRollsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env4ci")
	os.WriteFile(path, []byte("old"), 0o755)

	calls := 0
	rename = func(from, to string) error {
		if calls++; calls == 2 {
			return errors.New("disk full")
		}
		return os.Rename(from, to)
	}
	defer func() { rename = os.Rename }()

	if err := Replace(path, []byte("new")); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Fatalf("old binary not restored: %q", b)
	}
	if _, err := os.Stat(path + ".new"); err == nil {
		t.Fatal(".new left behind")
	}
}
