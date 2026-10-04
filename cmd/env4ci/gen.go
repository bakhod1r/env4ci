package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/ciscan"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/dotenv"
)

// loadAuthFile sets env4ci's own settings (tokens, addresses) from the auth
// file. Real environment variables win; unknown keys are refused so CI
// values never end up there by mistake.
func loadAuthFile(cfg config.Config, out io.Writer) error {
	path := cfg.AuthPath()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(out, "%s %s is readable by other users; run: chmod 600 %s\n", newPalette(out).Yellow("!"), path, path)
	}
	entries, err := dotenv.Parse(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, e := range entries {
		if !application.IsAuthKey(e.Key) {
			return fmt.Errorf("%s: %s is not an env4ci setting; CI variables belong in the environment files", path, e.Key)
		}
		if e.Value != "" && os.Getenv(e.Key) == "" {
			if err := os.Setenv(e.Key, e.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// cmdGen creates (or completes) the auth file and one .env file per
// environment from env4ci.yaml, pre-filling keys the CI files use.
// Existing values are never changed; only missing keys are appended.
func cmdGen(cfg config.Config, o opts, out io.Writer) error {
	targets := cfg.Targets.Names()
	if len(targets) == 0 {
		return errors.New("gen: no targets in env4ci.yaml (run env4ci init)")
	}
	base := filepath.Dir(o.config)
	ui := newPalette(out)

	// 1. env4ci's own settings.
	var authKeys []dotenv.ExampleKey
	for _, k := range application.AuthKeys(targets) {
		authKeys = append(authKeys, dotenv.ExampleKey{Key: k.Name, Kind: k.Comment})
	}
	authPath := cfg.AuthPath()
	if err := protect(base, authPath, out, ui); err != nil {
		return err
	}
	if err := writeOrAppend(filepath.Join(base, authPath), "env4ci settings (tokens, addresses). Never committed, never synced.", authKeys, out, ui); err != nil {
		return err
	}

	// 2. CI/CD environments.
	files := cfg.Environments
	if len(files) == 0 {
		files = map[string]string{"": first(cfg.Source, ".env")}
	}
	cl, err := cfg.Classifier()
	if err != nil {
		return err
	}
	refs, err := ciscan.Scan(first(o.dir, base), cl)
	if err != nil {
		return err
	}
	byEnv := map[string][]domain.Reference{}
	for _, g := range domain.GroupByEnvironment(refs) {
		byEnv[g.Environment] = g.Refs
	}

	envs := make([]string, 0, len(files))
	for e := range files {
		envs = append(envs, e)
	}
	sort.Strings(envs)
	for _, env := range envs {
		file := files[env]
		if file == authPath {
			return fmt.Errorf("gen: environment %q uses %s, which is the env4ci auth file", env, file)
		}
		// Shared keys are listed too: pushing them per environment is valid
		// (environment values override repository ones).
		var keys []dotenv.ExampleKey
		seen := map[string]bool{}
		for _, r := range append(append([]domain.Reference(nil), byEnv[env]...), byEnv[""]...) {
			if seen[r.Key] {
				continue
			}
			seen[r.Key] = true
			scope := "environment " + env
			if r.Environment == "" {
				scope = "shared"
			}
			keys = append(keys, dotenv.ExampleKey{Key: r.Key, Kind: r.Kind.String() + " · " + scope, Stages: r.Stages, Branches: r.Branches, Sources: r.Sources})
		}
		header := "CI/CD variables"
		if env != "" {
			header += " for environment " + env
		}
		if err := protect(base, file, out, ui); err != nil {
			return err
		}
		if err := writeOrAppend(filepath.Join(base, file), header, keys, out, ui); err != nil {
			return err
		}
	}
	if len(refs) == 0 {
		fmt.Fprintln(out, ui.Dim("no CI files found: environment files have no pre-filled keys"))
	}
	return nil
}

// writeOrAppend creates path (0600) with keys, or appends the keys it lacks.
func writeOrAppend(path, header string, keys []dotenv.ExampleKey, out io.Writer, ui palette) error {
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		werr := dotenv.WriteKeys(f, header, keys)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		fmt.Fprintf(out, "%s created %s (%d keys, mode 0600)\n", ui.Green("✓"), filepath.ToSlash(path), len(keys))
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := dotenv.Parse(strings.NewReader(string(existing)))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Key] = true
	}
	var missing []dotenv.ExampleKey
	for _, k := range keys {
		if !have[k.Key] {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintf(out, "%s %s is complete\n", ui.Dim("="), filepath.ToSlash(path))
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		_, _ = io.WriteString(f, "\n")
	}
	werr := dotenv.AppendKeys(f, missing)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	names := make([]string, len(missing))
	for i, k := range missing {
		names[i] = k.Key
	}
	fmt.Fprintf(out, "%s added %d keys to %s: %s\n", ui.Green("+"), len(missing), filepath.ToSlash(path), strings.Join(names, ", "))
	return nil
}

// protect makes sure file can never be committed before it is written.
// A file inside a folder: the folder is created (0700), gets its own
// ".gitignore" with "*", and "/<folder>/" goes into the root .gitignore.
// A file at the top level: the file itself goes into the root .gitignore.
func protect(base, file string, out io.Writer, ui palette) error {
	file = filepath.ToSlash(filepath.Clean(file))
	dir, _ := filepath.Split(file)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		return gitignoreAdd(base, file, out, ui)
	}
	top := strings.SplitN(dir, "/", 2)[0]
	if top == ".." || filepath.IsAbs(file) {
		return nil // outside the project: nothing to ignore here
	}
	full := filepath.Join(base, filepath.FromSlash(dir))
	if err := os.MkdirAll(full, 0o700); err != nil {
		return err
	}
	inner := filepath.Join(base, top, ".gitignore")
	if _, err := os.Stat(inner); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(inner, []byte("# generated by env4ci: nothing in this folder may be committed\n*\n"), 0o600); err != nil {
			return err
		}
	}
	return gitignoreAdd(base, "/"+top+"/", out, ui)
}

func gitignoreAdd(base, entry string, out io.Writer, ui palette) error {
	added, err := ensureGitignored(filepath.Join(base, ".gitignore"), entry)
	if err != nil {
		return err
	}
	if added {
		fmt.Fprintf(out, "%s added %s to .gitignore\n", ui.Green("✓"), entry)
	}
	return nil
}
