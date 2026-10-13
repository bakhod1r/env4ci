package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/httpx"
	"github.com/bakhod1r/env4ci/internal/infrastructure/selfupdate"
)

// releaseSource is where "env4ci update" finds releases; faked in tests.
type releaseSource interface {
	Latest(ctx context.Context) (tag string, err error)
	Download(ctx context.Context, tag, goos, goarch string) ([]byte, error)
}

// Update seams; tests replace them.
var (
	newReleaseSource = func() releaseSource {
		c := httpx.New()
		c.HTTP = &http.Client{Timeout: 5 * time.Minute} // a release archive, not an API call
		return selfupdate.Source{HTTP: c, BaseURL: selfupdate.DefaultBaseURL, Repo: selfupdate.DefaultRepo, MaxBytes: selfupdate.DefaultMaxBytes}
	}
	executable    = os.Executable
	replaceBinary = selfupdate.Replace
)

// packageManagerCommand is the upgrade command when path belongs to
// Homebrew or Scoop, which must do the update themselves; "" otherwise.
func packageManagerCommand(path string) string {
	p := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	switch {
	case strings.Contains(p, "/caskroom/"), strings.Contains(p, "/cellar/"), strings.Contains(p, "/homebrew/"):
		return "brew upgrade env4ci"
	case strings.Contains(p, "/scoop/"):
		return "scoop update env4ci"
	}
	return ""
}

// cmdUpdate replaces the running binary with the latest release when that
// is newer. --check only reports.
func cmdUpdate(ctx context.Context, o opts, out io.Writer, src releaseSource, goos, goarch string) error {
	ui := newPalette(out)
	current, err := domain.ParseVersion(version)
	if err != nil {
		return fmt.Errorf("update: this is a development build (%s), not a release; install one from https://github.com/%s/releases", version, selfupdate.DefaultRepo)
	}
	tag, err := src.Latest(ctx)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	latest, err := domain.ParseVersion(tag)
	if err != nil {
		return fmt.Errorf("update: latest release: %w", err)
	}
	if !current.Less(latest) {
		fmt.Fprintf(out, "%s env4ci %s is the latest version\n", ui.Green("✓"), current)
		return nil
	}
	if o.check {
		fmt.Fprintf(out, "%s env4ci %s is available (you have %s); run: env4ci update\n", ui.Yellow("!"), latest, current)
		return nil
	}
	path, err := executable()
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if cmd := packageManagerCommand(path); cmd != "" {
		fmt.Fprintf(out, "%s env4ci %s is available (you have %s); this copy is managed by a package manager, run: %s\n", ui.Yellow("!"), latest, current, cmd)
		return nil
	}
	fmt.Fprintf(out, "%s\n", ui.Dim(fmt.Sprintf("→ downloading env4ci %s (%s/%s)", latest, goos, goarch)))
	bin, err := src.Download(ctx, tag, goos, goarch)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if err := replaceBinary(path, bin); err != nil {
		return fmt.Errorf("update: cannot replace %s: %w (run with more rights, or reinstall)", path, err)
	}
	fmt.Fprintf(out, "%s updated env4ci %s -> %s (%s)\n", ui.Green("✓"), current, latest, path)
	return nil
}
