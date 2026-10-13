// Package selfupdate finds, downloads, verifies and installs env4ci
// release binaries from GitHub releases (the same files install.sh uses).
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
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
)

const (
	DefaultBaseURL = "https://github.com"
	DefaultRepo    = "bakhod1r/env4ci"
	// DefaultMaxBytes bounds every download and the extracted binary.
	DefaultMaxBytes = 200 << 20
)

// Doer is the HTTP client; it must follow redirects.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Source reads releases of Repo hosted at BaseURL.
type Source struct {
	HTTP     Doer
	BaseURL  string
	Repo     string
	MaxBytes int64
}

// Latest returns the newest release tag (e.g. "v0.3.1"): the page
// <repo>/releases/latest redirects to <repo>/releases/tag/<tag>.
func (s Source) Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/"+s.Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	tag := path.Base(resp.Request.URL.Path)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("could not find the latest release of %s (HTTP %d)", s.Repo, resp.StatusCode)
	}
	return tag, nil
}

// ArchiveName is the release asset for a platform, as goreleaser names it.
func ArchiveName(tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "env4ci_" + strings.TrimPrefix(tag, "v") + "_" + goos + "_" + goarch + ext
}

// Download returns the env4ci binary of release tag for goos/goarch, after
// checking the archive against the release's checksums.txt.
func (s Source) Download(ctx context.Context, tag, goos, goarch string) ([]byte, error) {
	name := ArchiveName(tag, goos, goarch)
	base := s.BaseURL + "/" + s.Repo + "/releases/download/" + tag + "/"
	sums, err := s.get(ctx, base+"checksums.txt")
	if err != nil {
		return nil, err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return nil, fmt.Errorf("%s is not in the %s release (no build for %s/%s)", name, tag, goos, goarch)
	}
	archive, err := s.get(ctx, base+name)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s: nothing was installed", name)
	}
	bin := "env4ci"
	if goos == "windows" {
		bin = "env4ci.exe"
	}
	return extract(name, archive, bin, s.MaxBytes)
}

func (s Source) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return readLimited(resp.Body, s.MaxBytes, url)
}

// readLimited reads all of r, refusing more than limit bytes.
func readLimited(r io.Reader, limit int64, what string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", what, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", what, limit)
	}
	return b, nil
}

// extract returns the file bin from a .zip or .tar.gz archive.
func extract(name string, archive []byte, bin string, limit int64) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, f := range zr.File {
			if f.Name != bin {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			defer rc.Close()
			return readLimited(rc, limit, bin)
		}
		return nil, fmt.Errorf("%s has no %s", name, bin)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s has no %s", name, bin)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if h.Name == bin {
			return readLimited(tr, limit, bin)
		}
	}
}

// rename is os.Rename; tests replace it to reach the rollback path.
var rename = os.Rename

// Replace installs bin at path: the new file is written next to it, the old
// binary is moved aside, and put back if the new one cannot take its place.
func Replace(path string, bin []byte) error {
	tmp, old := path+".new", path+".old"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil { // #nosec G306 -- an executable
		return err
	}
	if err := rename(path, old); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if err := rename(tmp, path); err != nil {
		return errors.Join(err, rename(old, path), os.Remove(tmp))
	}
	_ = os.Remove(old) // Windows keeps a running binary locked: it stays as .old
	return nil
}
