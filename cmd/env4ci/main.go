// Command env4ci syncs .env files with GitHub Actions and GitLab CI/CD.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/ciscan"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/dotenv"
	"github.com/bakhod1r/env4ci/internal/infrastructure/httpx"
	"github.com/bakhod1r/env4ci/internal/infrastructure/verify"
)

var version = "dev"

const usage = `env4ci — sync environment variables and secrets across CI/CD platforms

Usage:
  env4ci init                       write env4ci.yaml template
  env4ci validate                   parse and classify the local file
  env4ci diff  [github|gitlab]      show plan (never prints values)
  env4ci push  [github|gitlab]      apply plan after confirmation
  env4ci pull  [github|gitlab]      write readable remote values to a .env file

  Provider, repository and environment default to: git remote origin, and the
  current branch mapped through "branches:" in env4ci.yaml.
  env4ci verify                     log in with SSH keys / registry tokens from the .env file
  env4ci scan                       list variables CI files expect; --write creates .env examples
  env4ci version

Flags (after the subcommand):
  -c, --config   config file (default env4ci.yaml)
  -f, --file     local .env file (overrides config source)
  -e, --env      GitHub environment / GitLab environment scope
  --no-verify    push: skip SSH/registry login checks
  --shared       target repository level / scope "*" (ignore branches:)
  --repo         GitHub owner/name or GitLab project path
  --prune        push: delete remote keys missing locally
  -y, --yes      push: skip confirmation
  -o, --out      pull: output file (default .env.<env> or .env.pulled)
  --write        scan: write .env.example and .env.<group>.example (skips existing)
  --by           scan: group by "env" (default) or "branch"
  --dir          scan: project root (default .)
  --exit-code    diff: exit 2 when there are changes (for CI drift checks)

Tokens: GITHUB_TOKEN or GH_TOKEN (falls back to "gh auth token"), GITLAB_TOKEN.
GitLab URL: targets.gitlab.base_url, else GITLAB_URL / CI_SERVER_URL, else https://gitlab.com.

Exit codes: 0 ok, 1 error, 2 diff --exit-code found changes.
`

// errDrift signals diff --exit-code found changes.
var errDrift = errors.New("changes detected")

func main() {
	httpx.UserAgent = "env4ci/" + version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdin, os.Stdout)
	stop()
	switch {
	case err == nil:
	case errors.Is(err, errDrift):
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type opts struct {
	config, file, env, repo, out string
	dir, by                      string
	prune, yes, write, exitCode  bool
	shared, noVerify             bool
}

func parseFlags(args []string) (opts, []string, error) {
	var o opts
	fs := flag.NewFlagSet("env4ci", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, n := range []string{"c", "config"} {
		fs.StringVar(&o.config, n, config.DefaultFile, "")
	}
	for _, n := range []string{"f", "file"} {
		fs.StringVar(&o.file, n, "", "")
	}
	for _, n := range []string{"e", "env"} {
		fs.StringVar(&o.env, n, "", "")
	}
	for _, n := range []string{"o", "out"} {
		fs.StringVar(&o.out, n, "", "")
	}
	for _, n := range []string{"y", "yes"} {
		fs.BoolVar(&o.yes, n, false, "")
	}
	fs.StringVar(&o.repo, "repo", "", "")
	fs.BoolVar(&o.prune, "prune", false, "")
	fs.BoolVar(&o.write, "write", false, "")
	fs.StringVar(&o.dir, "dir", ".", "")
	fs.StringVar(&o.by, "by", "env", "")
	fs.BoolVar(&o.exitCode, "exit-code", false, "")
	fs.BoolVar(&o.shared, "shared", false, "")
	fs.BoolVar(&o.noVerify, "no-verify", false, "")

	// Allow flags before and after positional args.
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return o, nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return o, pos, nil
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(out, usage)
		return nil
	}
	cmd := args[0]
	o, pos, err := parseFlags(args[1:])
	if err != nil {
		return err
	}
	switch cmd {
	case "version":
		fmt.Fprintln(out, "env4ci", version)
		return nil
	case "init":
		return cmdInit(o, out)
	}

	cfg, err := config.Load(o.config)
	if err != nil {
		return err
	}
	switch cmd {
	case "validate":
		local, err := loadLocal(cfg, o)
		if err != nil {
			return err
		}
		printLocal(out, local)
		return nil
	case "scan":
		return cmdScan(cfg, o, out)
	case "verify":
		if o.file == "" && len(cfg.Branches) > 0 {
			if t, err := resolveTarget(cfg, o, nil, gitSource{dir: "."}); err == nil {
				o.file = t.File
			}
		}
		local, err := loadLocal(cfg, o)
		if err != nil {
			return err
		}
		checks := credentialChecks(cfg, local)
		if len(checks) == 0 {
			fmt.Fprintln(out, "No SSH keys or registry credentials found.")
			return nil
		}
		if err := runCredentialChecks(ctx, out, checks, nil); err != nil {
			return errors.New("credential check failed")
		}
		return nil
	case "diff", "plan", "push", "apply", "pull":
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if len(pos) > 1 {
		return fmt.Errorf("%s: at most one provider argument (github or gitlab)", cmd)
	}
	t, err := resolveTarget(cfg, o, pos, gitSource{dir: "."})
	if err != nil {
		return err
	}
	o.env, o.file = t.Environment, t.File
	fmt.Fprintln(out, newPalette(out).Dim(t.Describe()))
	p, err := newProvider(t)
	if err != nil {
		return err
	}
	svc := application.Service{Provider: p}
	if cmd == "pull" {
		return cmdPull(ctx, svc, o, out)
	}

	local, err := loadLocal(cfg, o)
	if err != nil {
		return err
	}
	remote, err := p.List(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", p.Name(), err)
	}
	plan, err := svc.Plan(ctx, local)
	if err != nil {
		return err
	}
	printPlan(out, p.Name(), plan, o.prune)
	if cmd == "diff" || cmd == "plan" {
		if o.exitCode && (plan.HasWrites() || (o.prune && hasRemoteOnly(plan))) {
			return errDrift
		}
		return nil
	}
	if !plan.HasWrites() && (!o.prune || !hasRemoteOnly(plan)) {
		fmt.Fprintln(out, "\nNothing to do.")
		return nil
	}
	if !o.noVerify {
		if err := runCredentialChecks(ctx, out, credentialChecks(cfg, local), application.TouchedKeys(plan)); err != nil {
			return err
		}
	}
	if !o.yes && !confirm(in, out, "\nApply these changes? [y/N] ") {
		return errors.New("aborted")
	}
	res, err := svc.Apply(ctx, local, plan, remote, application.ApplyOptions{Prune: o.prune})
	fmt.Fprintf(out, "\n%s %d written, %d deleted\n", newPalette(out).Green("✓"), res.Written, res.Deleted)
	return err
}

func cmdScan(cfg config.Config, o opts, out io.Writer) error {
	cl, err := cfg.Classifier()
	if err != nil {
		return err
	}
	refs, err := ciscan.Scan(o.dir, cl)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		fmt.Fprintln(out, "No CI variables found (looked in .github/workflows and .gitlab-ci.yml).")
		return nil
	}

	var grouped []domain.EnvGroup
	label, fileName := envLabel, envFile
	switch o.by {
	case "env", "environment":
		grouped = domain.GroupByEnvironment(refs)
	case "branch":
		grouped = domain.GroupByBranch(refs)
		label, fileName = branchLabel, branchFile
	default:
		return fmt.Errorf("--by: want env or branch, got %q", o.by)
	}

	// The same key from GitHub and GitLab is one line per group.
	var envs []string
	var merged []domain.Reference
	groups := map[string][]dotenv.ExampleKey{}
	for _, g := range grouped {
		envs = append(envs, g.Environment)
		merged = append(merged, g.Refs...)
		fmt.Fprintf(out, "\n%s\n", label(g.Environment))
		for _, r := range g.Refs {
			groups[g.Environment] = append(groups[g.Environment],
				dotenv.ExampleKey{Key: r.Key, Kind: r.Kind.String(), Stages: r.Stages, Branches: r.Branches, Sources: r.Sources})
			fmt.Fprintf(out, "  %-28s %-8s %-16s %-20s %s\n", r.Key, r.Kind,
				strings.Join(r.Stages, ","), strings.Join(r.Branches, ","), strings.Join(r.Sources, ", "))
		}
	}

	if o.file != "" {
		local, err := loadLocal(cfg, o)
		if err != nil {
			return err
		}
		if missing := domain.MissingFrom(merged, local); len(missing) > 0 {
			fmt.Fprintf(out, "\n! missing from %s:\n", o.file)
			for _, m := range missing {
				fmt.Fprintf(out, "  %s (%s, stages: %s)\n", m.Key, label(m.Environment), strings.Join(m.Stages, ","))
			}
		} else {
			fmt.Fprintf(out, "\n✓ %s has every key CI uses\n", o.file)
		}
	}

	if !o.write {
		return nil
	}
	fmt.Fprintln(out)
	used := map[string]int{}
	for _, env := range envs {
		// Distinct groups can slug to one name ("release/*", "/^release\//").
		name := fileName(env)
		if n := used[name]; n > 0 {
			name = strings.TrimSuffix(name, ".example") + fmt.Sprintf("-%d.example", n+1)
		}
		used[fileName(env)]++
		path := filepath.Join(o.dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			fmt.Fprintf(out, "- skipped %s (exists)\n", path)
			continue
		}
		if err != nil {
			return err
		}
		err = dotenv.WriteExample(f, label(env), groups[env])
		f.Close()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ wrote %s (%d keys)\n", path, len(groups[env]))
	}
	return nil
}

func envFile(env string) string {
	if env == "" {
		return ".env.example"
	}
	return ".env." + slug(env) + ".example"
}

func branchLabel(b string) string {
	switch b {
	case domain.BranchAll:
		return "all branches"
	case domain.BranchTags:
		return "tags"
	}
	return "branch: " + b
}

func branchFile(b string) string {
	if b == domain.BranchAll {
		return ".env.example"
	}
	return ".env." + slug(b) + ".example"
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// slug makes a branch or environment usable as a file name part:
// "release/*" -> "release", "/^release\//" -> "release", "(tags)" -> "tags".
func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-.")
	if s == "" {
		return "branch"
	}
	return s
}

func envLabel(env string) string {
	if env == "" {
		return "shared (repository / scope *)"
	}
	return "environment: " + env
}

func cmdInit(o opts, out io.Writer) error {
	f, err := os.OpenFile(o.config, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.WriteString(f, config.Template); err != nil {
		return err
	}
	fmt.Fprintln(out, "✓ wrote", o.config)
	return nil
}

func loadLocal(cfg config.Config, o opts) ([]domain.Variable, error) {
	path := o.file
	if path == "" {
		path = cfg.Source
	}
	if path == "" {
		path = ".env"
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := dotenv.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cl, err := cfg.Classifier()
	if err != nil {
		return nil, err
	}
	vars := make([]domain.Variable, 0, len(entries))
	for _, e := range entries {
		if err := domain.ValidateKey(e.Key); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		vars = append(vars, domain.Variable{Key: e.Key, Value: e.Value, Kind: cl.Classify(e.Key)})
	}
	return vars, nil
}

func cmdPull(ctx context.Context, svc application.Service, o opts, out io.Writer) error {
	known, hidden, err := svc.Pull(ctx)
	if err != nil {
		return err
	}
	path := o.out
	if path == "" {
		path = ".env.pulled"
		if o.env != "" {
			path = ".env." + o.env
		}
	}
	entries := make([]dotenv.Entry, 0, len(known))
	for _, r := range known {
		entries = append(entries, dotenv.Entry{Key: r.Key, Value: r.Value})
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%w (refusing to overwrite; use --out)", err)
	}
	defer f.Close()
	if err := dotenv.Write(f, entries); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ wrote %d values to %s (mode 0600)\n", len(entries), path)
	if len(hidden) > 0 {
		fmt.Fprintf(out, "! %d secrets are write-only on this provider and were skipped: %s\n", len(hidden), strings.Join(hidden, ", "))
	}
	if added, err := ensureGitignored(".gitignore", path); err == nil && added {
		fmt.Fprintf(out, "✓ added %s to .gitignore\n", path)
	}
	return nil
}

func ensureGitignored(gitignore, path string) (bool, error) {
	b, err := os.ReadFile(gitignore)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == path {
			return false, nil
		}
	}
	f, err := os.OpenFile(gitignore, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	prefix := ""
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		prefix = "\n"
	}
	_, err = fmt.Fprintf(f, "%s%s\n", prefix, path)
	return err == nil, err
}

func printLocal(out io.Writer, vars []domain.Variable) {
	fmt.Fprintf(out, "✓ loaded %d variables\n", len(vars))
	for _, k := range []domain.Kind{domain.KindSecret, domain.KindVariable} {
		title := "Variables"
		if k == domain.KindSecret {
			title = "Secrets"
		}
		fmt.Fprintf(out, "\n%s\n", title)
		for _, v := range vars {
			if v.Kind == k {
				fmt.Fprintf(out, "  %s\n", v.Key)
			}
		}
	}
}

func printPlan(out io.Writer, target string, p domain.Plan, prune bool) {
	ui := newPalette(out)
	fmt.Fprintf(out, "%s\n\n", ui.Bold(target))
	for _, c := range p.Changes {
		label := c.Action.String()
		if c.Action == domain.ActionRemoteOnly && prune {
			label = "delete"
		}
		line := fmt.Sprintf("  %s %-32s %-8s %s", c.Action.Symbol(), c.Key, c.Kind, label)
		switch c.Action {
		case domain.ActionCreate:
			line = ui.Green(line)
		case domain.ActionUpdate:
			line = ui.Yellow(line)
		case domain.ActionUnverifiable:
			line = ui.Purple(line)
		case domain.ActionRemoteOnly:
			if prune {
				line = ui.Red(line)
			} else {
				line = ui.Dim(line)
			}
		default:
			line = ui.Dim(line)
		}
		fmt.Fprintln(out, line)
	}
}

// credentialChecks merges auto-detected credentials with env4ci.yaml checks:.
// A configured entry replaces a detected one for the same key/password.
func credentialChecks(cfg config.Config, vars []domain.Variable) []application.Check {
	sshByKey := map[string]domain.SSHCredential{}
	var sshOrder []string
	add := func(c domain.SSHCredential) {
		if _, ok := sshByKey[c.Key]; !ok {
			sshOrder = append(sshOrder, c.Key)
		}
		sshByKey[c.Key] = c
	}
	for _, c := range domain.DetectSSH(vars) {
		add(c)
	}
	for _, c := range cfg.Checks.SSH {
		add(domain.SSHCredential{Key: c.Key, Host: c.Host, User: c.User, Port: c.Port, KnownHosts: c.KnownHosts})
	}
	var sshCreds []domain.SSHCredential
	for _, k := range sshOrder {
		sshCreds = append(sshCreds, sshByKey[k])
	}

	regByPw := map[string]domain.RegistryCredential{}
	var regOrder []string
	addReg := func(c domain.RegistryCredential) {
		if _, ok := regByPw[c.Password]; !ok {
			regOrder = append(regOrder, c.Password)
		}
		regByPw[c.Password] = c
	}
	for _, c := range domain.DetectRegistry(vars) {
		addReg(c)
	}
	for _, c := range cfg.Checks.Registry {
		addReg(domain.RegistryCredential{Registry: c.Registry, Username: c.Username, Password: c.Password})
	}
	var regCreds []domain.RegistryCredential
	for _, k := range regOrder {
		regCreds = append(regCreds, regByPw[k])
	}
	return verify.Checks(vars, sshCreds, regCreds)
}

// runCredentialChecks prints results and returns an error if any failed.
func runCredentialChecks(ctx context.Context, out io.Writer, checks []application.Check, touched map[string]bool) error {
	results := application.RunChecks(ctx, checks, touched)
	if len(results) == 0 {
		return nil
	}
	ui := newPalette(out)
	fmt.Fprintf(out, "\n%s\n", ui.Bold("Credential checks"))
	for _, r := range results {
		if r.Err != nil {
			fmt.Fprintf(out, "  %s %s: %v\n", ui.Red("✗"), r.Name, r.Err)
		} else {
			fmt.Fprintf(out, "  %s %s\n", ui.Green("✓"), r.Name)
		}
	}
	if application.AnyFailed(results) {
		return errCheckFailed
	}
	return nil
}

var errCheckFailed = errors.New("credential check failed; nothing was written (fix it, or --no-verify to skip)")

func hasRemoteOnly(p domain.Plan) bool {
	for _, c := range p.Changes {
		if c.Action == domain.ActionRemoteOnly {
			return true
		}
	}
	return false
}

func confirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// ghCLIToken asks the GitHub CLI for its token; empty if gh is absent.
func ghCLIToken() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
