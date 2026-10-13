package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/ciscan"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/dotenv"
	"github.com/bakhod1r/env4ci/internal/infrastructure/gitinfo"
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
			_ = os.Setenv(e.Key, e.Value) // key is a known setting name: cannot be invalid
		}
	}
	return nil
}

// cmdGen creates (or completes) the auth file and one .env file per
// environment from env4ci.yaml, pre-filling keys the CI files use.
// Existing values are never changed; only missing keys are appended.
func cmdGen(cfg config.Config, o opts, in io.Reader, out io.Writer) error {
	targets := cfg.Targets.Names()
	if len(targets) == 0 {
		return errors.New("gen: no targets in env4ci.yaml (run env4ci init)")
	}
	base := filepath.Dir(o.config)
	ui := newPalette(out)

	cl, err := cfg.Classifier()
	if err != nil {
		return err
	}
	refs, err := ciscan.Scan(first(o.dir, base), cl)
	if err != nil {
		return err
	}
	if len(cfg.Environments) > 0 && !o.fixedEnvs {
		if cfg, err = addCIEnvironments(cfg, o.config, refs, out, ui); err != nil {
			return err
		}
		if cfg, err = removeGoneEnvironments(cfg, o, in, refs, out, ui); err != nil {
			return err
		}
	}

	// 1. env4ci's own settings.
	var authKeys []dotenv.ExampleKey
	for _, k := range application.AuthKeys(targets) {
		authKeys = append(authKeys, dotenv.ExampleKey{Key: k.Name, Kind: k.Comment, Group: k.Target})
	}
	authPath := cfg.AuthPath()
	// Validate every target path before anything is written.
	for _, f := range append([]string{authPath}, mapValues(cfg.Environments)...) {
		if err := keepConfigTracked(o.config, filepath.Join(base, f)); err != nil {
			return err
		}
	}
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
	// dynamic: jobs whose environment is decided at run time
	// (${{ github.ref_name }}); their keys can belong to any environment.
	byEnv := map[string][]domain.Reference{}
	var dynamic []domain.Reference
	for _, g := range domain.GroupByEnvironment(refs) {
		byEnv[g.Environment] = g.Refs
		if g.Environment != "" && !keyLike(g.Environment) {
			dynamic = append(dynamic, g.Refs...)
		}
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
		keys := envFileKeys(env, append(append(append([]domain.Reference(nil), byEnv[env]...), dynamic...), byEnv[""]...))
		if env != "" {
			keys = withEnvironmentName(env, cl.Classify(environmentKey), keys)
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
	if gitinfo.IsIgnored(base, o.config) {
		fmt.Fprintf(out, "%s %s is gitignored; it holds no secrets and should be committed (check .gitignore)\n", ui.Yellow("!"), o.config)
	}
	return nil
}

// addCIEnvironments adds to env4ci.yaml every environment the CI files
// deploy to that it lacks, and returns cfg with them. Only the new lines are
// inserted: comments and everything else in the file stay as they are. A
// config whose environments: or branches: is not a plain block is left alone.
func addCIEnvironments(cfg config.Config, configPath string, refs []domain.Reference, out io.Writer, ui palette) (config.Config, error) {
	files, branches := copyMap(cfg.Environments), copyMap(cfg.Branches)
	var names, notes, envLines, branchLines []string
	for _, e := range ciEnvironments(refs) {
		if _, known := files[e.Name]; known {
			continue
		}
		if _, mapped := domain.BranchMap(cfg.Branches).Environment(e.Branch); e.FromBranch && mapped {
			continue // that branch already has an environment, under another name
		}
		file := newEnvFile(cfg.Environments, e.Name)
		files[e.Name] = file
		envLines = append(envLines, e.Name+": "+file)
		note := "file " + file
		if len(cfg.Branches) > 0 {
			for _, b := range []string{first(e.Branch, branchFor(e.Name)), e.Name} {
				if _, taken := branches[b]; !taken {
					branches[b] = e.Name
					branchLines = append(branchLines, fmt.Sprintf("%q: %s", b, e.Name))
					note = "branch " + b + ", " + note
					break
				}
			}
		}
		names = append(names, e.Name)
		notes = append(notes, note)
	}
	if len(names) == 0 {
		return cfg, nil
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		return cfg, err
	}
	text, ok := addYAMLEntries(string(b), "environments", envLines)
	if ok && len(branchLines) > 0 {
		text, ok = addYAMLEntries(text, "branches", branchLines)
	}
	if !ok {
		fmt.Fprintf(out, "%s CI deploys to %s, which %s lacks; add under environments: by hand\n", ui.Yellow("!"), strings.Join(names, ", "), configPath)
		return cfg, nil
	}
	f, err := openFile(configPath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return cfg, err
	}
	if err := writeClose(f, []byte(text)); err != nil {
		return cfg, err
	}
	for i, n := range names {
		fmt.Fprintf(out, "%s added environment %s to %s (%s)\n", ui.Green("+"), n, filepath.ToSlash(configPath), notes[i])
	}
	cfg.Environments, cfg.Branches = files, branches
	return cfg, nil
}

// removeGoneEnvironments handles every environment of env4ci.yaml the CI
// files no longer deploy to (a removed environment: or branch). With -y, or
// a "y" on a terminal, its lines leave env4ci.yaml, its env file is deleted
// and its .gitignore line goes; otherwise it is only named. cfg is returned
// without the removed environments.
func removeGoneEnvironments(cfg config.Config, o opts, in io.Reader, refs []domain.Reference, out io.Writer, ui palette) (config.Config, error) {
	ci := ciEnvironments(refs)
	if len(ci) == 0 {
		return cfg, nil // CI names no environment at all: nothing to compare
	}
	live := map[string]bool{}
	for _, e := range ci {
		live[e.Name] = true
		if env, ok := domain.BranchMap(cfg.Branches).Environment(e.Branch); ok && e.Branch != "" {
			live[env] = true // the branch deploys there under the config's name
		}
	}
	names := make([]string, 0, len(cfg.Environments))
	for n := range cfg.Environments {
		names = append(names, n)
	}
	sort.Strings(names)
	base := filepath.Dir(o.config)
	answers := bufio.NewReader(orEmpty(in))
	for _, n := range names {
		if live[n] {
			continue
		}
		file := filepath.ToSlash(cfg.Environments[n])
		var branches []string
		for b, e := range cfg.Branches {
			if e == n {
				branches = append(branches, b)
			}
		}
		sort.Strings(branches)
		what := "environment " + n
		if len(branches) > 0 {
			what += " (branch " + strings.Join(branches, ", ") + ")"
		}
		if filled := filledValues(filepath.Join(base, file)); filled > 0 {
			file += fmt.Sprintf(", which has %d filled values", filled)
		}
		fmt.Fprintf(out, "%s CI no longer deploys to %s\n", ui.Yellow("!"), what)
		remove := o.yes
		if !remove && o.interactive {
			remove = confirm(answers, out, fmt.Sprintf("  remove it from %s and delete %s? [y/N] ", filepath.ToSlash(o.config), file))
		}
		if !remove {
			fmt.Fprintf(out, "  kept; to remove it run %s, or answer y on a terminal\n", ui.Bold("env4ci gen -y"))
			continue
		}
		if err := removeEnvironment(cfg, o.config, n, out, ui); err != nil {
			return cfg, err
		}
		cfg.Environments, cfg.Branches = copyMap(cfg.Environments), copyMap(cfg.Branches)
		delete(cfg.Environments, n)
		for _, b := range branches {
			delete(cfg.Branches, b)
		}
	}
	return cfg, nil
}

func orEmpty(r io.Reader) io.Reader {
	if r == nil {
		return strings.NewReader("")
	}
	return r
}

// filledValues counts the keys of an env file that have a value, leaving
// out the generated ENVIRONMENT name. 0 when the file cannot be read.
func filledValues(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	entries, _ := dotenv.Parse(f)
	n := 0
	for _, e := range entries {
		if e.Value != "" && e.Key != environmentKey {
			n++
		}
	}
	return n
}

// removeEnvironment drops env's lines from env4ci.yaml (environments: and
// the branches: that map to it), its .gitignore line, and its env file.
func removeEnvironment(cfg config.Config, configPath, env string, out io.Writer, ui palette) error {
	b, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	text, ok := removeYAMLEntries(string(b), "environments", func(k, _ string) bool { return k == env })
	if ok && strings.Contains(text, "\nbranches:") || strings.HasPrefix(text, "branches:") {
		text, ok = removeYAMLEntries(text, "branches", func(_, v string) bool { return v == env })
	}
	if !ok {
		fmt.Fprintf(out, "  %s is not in plain block style: remove %s from it by hand\n", filepath.ToSlash(configPath), env)
		return nil
	}
	f, err := openFile(configPath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if err := writeClose(f, []byte(text)); err != nil {
		return err
	}
	base := filepath.Dir(configPath)
	file := filepath.ToSlash(filepath.Clean(cfg.Environments[env]))
	entry := file
	if strings.Contains(file, "/") {
		entry = "/" + file
	}
	if err := gitignoreRemove(filepath.Join(base, ".gitignore"), entry); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(base, filepath.FromSlash(file))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Fprintf(out, "%s removed environment %s: %s line, %s, .gitignore entry\n", ui.Red("-"), env, filepath.ToSlash(configPath), file)
	return nil
}

// gitignoreRemove drops the line entry from a .gitignore; a missing file or
// line is fine.
func gitignoreRemove(path, entry string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(b), "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != entry {
			kept = append(kept, l)
		}
	}
	if len(kept) == len(lines) {
		return nil
	}
	f, err := openFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	return writeClose(f, []byte(strings.Join(kept, "")))
}

// removeYAMLEntries deletes the entries of the top-level block mapping key
// for which drop(key, value) is true. ok is false when text has no such
// block (the key is missing or written in flow style).
func removeYAMLEntries(text, key string, drop func(k, v string) bool) (string, bool) {
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	head := regexp.MustCompile(`^` + regexp.QuoteMeta(key) + `:\s*(#.*)?$`)
	for i, l := range lines {
		if !head.MatchString(l) {
			continue
		}
		kept := append([]string(nil), lines[:i+1]...)
		j := i + 1
		for ; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t != "" && !strings.HasPrefix(t, "#") && lines[j][0] != ' ' {
				break
			}
			if c := strings.Index(t, ":"); c > 0 && !strings.HasPrefix(t, "#") {
				v := strings.TrimSpace(t[c+1:])
				if h := strings.Index(v, " #"); h >= 0 {
					v = v[:h]
				}
				if drop(strings.Trim(t[:c], `"' `), strings.Trim(v, `"' `)) {
					continue
				}
			}
			kept = append(kept, lines[j])
		}
		return strings.Join(append(kept, lines[j:]...), eol), true
	}
	return text, false
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// newEnvFile names env's file the way the config names the others:
// production: .github/production.env gives .github/staging.env. When no
// file carries its environment's name, the folder of the first one is used.
func newEnvFile(existing map[string]string, env string) string {
	names := make([]string, 0, len(existing))
	for n := range existing {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := filepath.ToSlash(existing[n])
		if strings.Contains(path.Base(f), n) {
			return path.Join(path.Dir(f), strings.Replace(path.Base(f), n, env, 1))
		}
	}
	dir := path.Dir(filepath.ToSlash(existing[names[0]]))
	if dir == "." {
		dir = ""
	}
	return envFileFor(dir, env)
}

// addYAMLEntries inserts entries at the end of the top-level block mapping
// key, with the indentation its entries use. ok is false when text has no
// such block (the key is missing or written in flow style).
func addYAMLEntries(text, key string, entries []string) (string, bool) {
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	head := regexp.MustCompile(`^` + regexp.QuoteMeta(key) + `:\s*(#.*)?$`)
	for i, l := range lines {
		if !head.MatchString(l) {
			continue
		}
		last, indent := i, "  "
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if lines[j][0] != ' ' {
				break
			}
			if last == i {
				indent = lines[j][:len(lines[j])-len(strings.TrimLeft(lines[j], " "))]
			}
			last = j
		}
		added := make([]string, 0, len(entries))
		for _, e := range entries {
			added = append(added, indent+e)
		}
		lines = append(lines[:last+1], append(added, lines[last+1:]...)...)
		return strings.Join(lines, eol), true
	}
	return text, false
}

// environmentKey is the first key of every environment file: its value is
// the environment's name, so the file says where it deploys to.
const environmentKey = "ENVIRONMENT"

// withEnvironmentName puts environmentKey=env first, replacing a CI
// reference to the same key so it is written once.
func withEnvironmentName(env string, kind domain.Kind, keys []dotenv.ExampleKey) []dotenv.ExampleKey {
	out := []dotenv.ExampleKey{{Key: environmentKey, Value: env, Kind: kind.String() + " · environment name", Group: "environment name"}}
	for _, k := range keys {
		if k.Key != environmentKey {
			out = append(out, k)
		}
	}
	return out
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// envFileKeys orders an environment file: SSH credentials together, then
// registry credentials, then environment keys, then shared keys. An SSH key
// gets its known-hosts variable (read by env4ci verify) if CI lacks one.
func envFileKeys(env string, refs []domain.Reference) []dotenv.ExampleKey {
	var keys []dotenv.ExampleKey
	seen := map[string]bool{}
	for _, r := range refs {
		if seen[r.Key] {
			continue
		}
		seen[r.Key] = true
		scope := "environment " + env
		if r.Environment == "" {
			scope = "shared"
		}
		group := domain.Family(r.Key)
		if group == "" {
			group = scope
		}
		keys = append(keys, dotenv.ExampleKey{Key: r.Key, Kind: r.Kind.String() + " · " + scope,
			Stages: r.Stages, Branches: r.Branches, Sources: r.Sources, Group: group})
	}
	for _, k := range append([]dotenv.ExampleKey(nil), keys...) {
		kh := domain.KnownHostsFor(k.Key)
		if kh == "" || seen[kh] {
			continue
		}
		seen[kh] = true
		keys = append(keys, dotenv.ExampleKey{Key: kh, Group: "ssh",
			Kind: "secret · for env4ci verify: host key of the SSH server (ssh-keyscan -p <port> <host>)"})
	}
	rank := func(g string) int {
		switch g {
		case "ssh":
			return 0
		case "registry":
			return 1
		case "shared":
			return 3
		}
		return 2
	}
	sort.SliceStable(keys, func(i, j int) bool { return rank(keys[i].Group) < rank(keys[j].Group) })
	return keys
}

// commentedKey matches a disabled "# KEY=..." line; such keys count as present.
var commentedKey = regexp.MustCompile(`(?m)^\s*#\s*([A-Za-z_][A-Za-z0-9_]*)=`)

// writeOrAppend creates path (0600) with keys, or appends the keys it lacks.
func writeOrAppend(path, header string, keys []dotenv.ExampleKey, out io.Writer, ui palette) error {
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		var b strings.Builder
		_ = dotenv.WriteKeys(&b, header, keys) // strings.Builder never fails
		if err := writeNew(path, []byte(b.String()), 0o600); err != nil {
			return err
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
	for _, m := range commentedKey.FindAllStringSubmatch(string(existing), -1) {
		have[m[1]] = true // the user disabled it on purpose
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
	var b strings.Builder
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	_ = dotenv.AppendKeys(&b, missing) // strings.Builder never fails
	if err := appendFile(path, []byte(b.String())); err != nil {
		return err
	}
	names := make([]string, len(missing))
	for i, k := range missing {
		names[i] = k.Key
	}
	fmt.Fprintf(out, "%s added %d keys to %s: %s\n", ui.Green("+"), len(missing), filepath.ToSlash(path), strings.Join(names, ", "))
	return nil
}

// openFile is os.OpenFile; tests replace it to reach write failures.
var openFile = os.OpenFile

// writeNew creates path (failing if it exists) and writes b.
func writeNew(path string, b []byte, perm os.FileMode) error {
	f, err := openFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	return writeClose(f, b)
}

// appendFile appends b to an existing path.
func appendFile(path string, b []byte) error {
	f, err := openFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	return writeClose(f, b)
}

func writeClose(f *os.File, b []byte) error {
	_, werr := f.Write(b)
	cerr := f.Close()
	return errors.Join(werr, cerr)
}

// keepConfigTracked refuses a generated file that is env4ci.yaml itself:
// writing keys into it and gitignoring it would remove the config from git.
func keepConfigTracked(configPath, file string) error {
	cfgAbs, err1 := filepath.Abs(configPath)
	fileAbs, err2 := filepath.Abs(file)
	if err1 == nil && err2 == nil && cfgAbs == fileAbs {
		return fmt.Errorf("%s is the env4ci config; it must stay in git, use another file name", file)
	}
	return nil
}

// protect makes sure file can never be committed before it is written:
// the exact file path goes into the root .gitignore (never its folder, so
// .github/ and .gitlab/ stay tracked) and missing parent folders are created.
func protect(base, file string, out io.Writer, ui palette) error {
	file = filepath.ToSlash(filepath.Clean(file))
	if strings.HasPrefix(file, "..") || filepath.IsAbs(file) {
		return nil // outside the project: nothing to ignore here
	}
	if dir := filepath.Dir(filepath.Join(base, filepath.FromSlash(file))); dir != base {
		if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- .github/.gitlab hold committed files
			return err
		}
	}
	entry := file
	if strings.Contains(file, "/") {
		entry = "/" + file // anchored: only this path
	}
	return gitignoreAdd(base, entry, out, ui)
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
