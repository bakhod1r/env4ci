package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/gitinfo"
)

// errLeak signals leaks found secret values in tracked files (exit 2).
var errLeak = errors.New("secret values found in tracked files")

// maxLeakFile skips large tracked files (lockfiles, assets).
const maxLeakFile = 2 << 20

// leakSources lists the local env files whose secret values are searched for.
func leakSources(cfg config.Config, o opts) []string {
	set := map[string]bool{}
	for _, p := range append([]string{o.file, cfg.Source, ".env", cfg.AuthPath()}, mapValues(cfg.Environments)...) {
		if p != "" {
			set[filepath.Clean(p)] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// cmdLeaks fails when a tracked (or, with --staged, staged) file contains
// the value of a secret from a local env file. Only file:line and the key
// are printed.
func cmdLeaks(cfg config.Config, o opts, out io.Writer) error {
	sources := leakSources(cfg, o)
	var secrets []domain.Variable
	for _, p := range sources {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		oo := o
		oo.file = p
		vars, err := loadLocal(cfg, oo)
		if err != nil {
			return err
		}
		secrets = append(secrets, vars...)
	}
	pal := newPalette(out)
	if len(secrets) == 0 {
		fmt.Fprintln(out, "No secret values to look for (no local env files found).")
		return nil
	}
	files, err := gitinfo.Files(".", o.staged)
	if err != nil {
		return err
	}
	skip := map[string]bool{}
	for _, p := range sources {
		skip[p] = true
	}
	var leaks []domain.Leak
	for _, f := range files {
		if skip[filepath.Clean(f)] {
			continue
		}
		b, err := gitinfo.Content(".", f, o.staged)
		if err != nil || len(b) > maxLeakFile || bytes.IndexByte(b, 0) >= 0 {
			continue // deleted in worktree, too big, or binary
		}
		leaks = append(leaks, domain.FindLeaks(secrets, f, b)...)
	}
	scope := map[bool]string{true: "staged", false: "tracked"}[o.staged]
	if len(leaks) == 0 {
		fmt.Fprintf(out, "%s no secret values in %d %s files\n", pal.Green("✓"), len(files), scope)
		return nil
	}
	for _, l := range leaks {
		fmt.Fprintf(out, "%s %s:%d  value of %s\n", pal.Red("✗"), l.File, l.Line, l.Key)
	}
	fmt.Fprintf(out, "\n%d secret value(s) in %s files. Remove them; if already pushed, rotate the secret.\n", len(leaks), scope)
	return errLeak
}

const (
	hookBegin = "# >>> env4ci"
	hookLine  = "env4ci leaks --staged"
)

const hookScript = "#!/bin/sh\n" + hookBegin + ` pre-commit (added by "env4ci hook")
if command -v env4ci >/dev/null 2>&1; then
  ` + hookLine + ` || exit $?
else
  echo "env4ci not on PATH; skipping secret leak check" >&2
fi
# <<< env4ci
`

// cmdHook installs the pre-commit hook. An existing hook that is not
// env4ci's is left alone; the user adds one line to it.
func cmdHook(out io.Writer) error {
	dir, err := gitinfo.HooksDir(".")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "pre-commit")
	old, err := os.ReadFile(path)
	switch {
	case err == nil && string(old) == hookScript:
		fmt.Fprintf(out, "%s %s is up to date\n", newPalette(out).Dim("="), path)
		return nil
	case err == nil && !strings.Contains(string(old), hookBegin):
		return fmt.Errorf("%s already exists; add this line to it: %s", path, hookLine)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- git hooks dir
		return err
	}
	if err := os.WriteFile(path, []byte(hookScript), 0o755); err != nil { // #nosec G306 -- hook must be executable
		return err
	}
	fmt.Fprintf(out, "%s installed %s (runs %q before each commit)\n", newPalette(out).Green("✓"), path, hookLine)
	return nil
}
