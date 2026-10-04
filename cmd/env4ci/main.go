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
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/ciscan"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/dotenv"
	"github.com/bakhod1r/env4ci/internal/infrastructure/provider/github"
	"github.com/bakhod1r/env4ci/internal/infrastructure/provider/gitlab"
)

var version = "dev"

const usage = `env4ci — sync environment variables and secrets across CI/CD platforms

Usage:
  env4ci init                       write env4ci.yaml template
  env4ci validate                   parse and classify the local file
  env4ci diff  <github|gitlab>      show plan (never prints values)
  env4ci push  <github|gitlab>      apply plan after confirmation
  env4ci pull  <github|gitlab>      write readable remote values to a .env file
  env4ci scan                       list variables CI files expect; --write creates .env examples
  env4ci version

Flags (after the subcommand):
  -c, --config   config file (default env4ci.yaml)
  -f, --file     local .env file (overrides config source)
  -e, --env      GitHub environment / GitLab environment scope
  --repo         GitHub owner/name or GitLab project path
  --prune        push: delete remote keys missing locally
  -y, --yes      push: skip confirmation
  -o, --out      pull: output file (default .env.<env> or .env.pulled)
  --write        scan: write .env.example and .env.<env>.example (skips existing)
  --dir          scan: project root (default .)

Tokens: GITHUB_TOKEN (or GH_TOKEN), GITLAB_TOKEN.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type opts struct {
	config, file, env, repo, out string
	dir                          string
	prune, yes, write            bool
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
	case "diff", "plan", "push", "apply", "pull":
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if len(pos) != 1 {
		return fmt.Errorf("%s: provider required (github or gitlab)", cmd)
	}
	p, err := newProvider(pos[0], cfg, o)
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
		return nil
	}
	if !plan.HasWrites() && !(o.prune && hasRemoteOnly(plan)) {
		fmt.Fprintln(out, "\nNothing to do.")
		return nil
	}
	if !o.yes && !confirm(in, out, "\nApply these changes? [y/N] ") {
		return errors.New("aborted")
	}
	res, err := svc.Apply(ctx, local, plan, remote, application.ApplyOptions{Prune: o.prune})
	fmt.Fprintf(out, "\n✓ %d written, %d deleted\n", res.Written, res.Deleted)
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

	// The same key from GitHub and GitLab is one line per environment.
	var envs []string
	var merged []domain.Reference
	groups := map[string][]dotenv.ExampleKey{}
	for _, g := range domain.GroupByEnvironment(refs) {
		envs = append(envs, g.Environment)
		merged = append(merged, g.Refs...)
		fmt.Fprintf(out, "\n%s\n", envLabel(g.Environment))
		for _, r := range g.Refs {
			groups[g.Environment] = append(groups[g.Environment],
				dotenv.ExampleKey{Key: r.Key, Kind: r.Kind.String(), Stages: r.Stages, Sources: r.Sources})
			fmt.Fprintf(out, "  %-28s %-8s %-18s %s\n", r.Key, r.Kind, strings.Join(r.Stages, ","), strings.Join(r.Sources, ", "))
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
				fmt.Fprintf(out, "  %s (%s, stages: %s)\n", m.Key, envLabel(m.Environment), strings.Join(m.Stages, ","))
			}
		} else {
			fmt.Fprintf(out, "\n✓ %s has every key CI uses\n", o.file)
		}
	}

	if !o.write {
		return nil
	}
	fmt.Fprintln(out)
	for _, env := range envs {
		name := ".env.example"
		if env != "" {
			name = ".env." + env + ".example"
		}
		path := filepath.Join(o.dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			fmt.Fprintf(out, "- skipped %s (exists)\n", path)
			continue
		}
		if err != nil {
			return err
		}
		err = dotenv.WriteExample(f, envLabel(env), groups[env])
		f.Close()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ wrote %s (%d keys)\n", path, len(groups[env]))
	}
	return nil
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

func newProvider(name string, cfg config.Config, o opts) (application.Provider, error) {
	switch name {
	case "github":
		t := cfg.Targets.GitHub
		if t == nil {
			t = &config.GitHub{}
		}
		c := &github.Client{BaseURL: t.BaseURL, Repo: first(o.repo, t.Repo), Environment: first(o.env, t.Environment)}
		c.Token = first(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))
		if c.Repo == "" {
			return nil, errors.New("github: repo not set (--repo or targets.github.repo)")
		}
		if c.Token == "" {
			return nil, errors.New("github: GITHUB_TOKEN not set")
		}
		return c, nil
	case "gitlab":
		t := cfg.Targets.GitLab
		if t == nil {
			t = &config.GitLab{}
		}
		c := &gitlab.Client{BaseURL: t.BaseURL, Project: first(o.repo, t.Project), Environment: first(o.env, t.Environment), Protected: t.Protected}
		c.Token = os.Getenv("GITLAB_TOKEN")
		if c.Project == "" {
			return nil, errors.New("gitlab: project not set (--repo or targets.gitlab.project)")
		}
		if c.Token == "" {
			return nil, errors.New("gitlab: GITLAB_TOKEN not set")
		}
		return c, nil
	}
	return nil, fmt.Errorf("unknown provider %q (want github or gitlab)", name)
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
		fmt.Fprintf(out, "\n%ss\n", strings.Title(k.String())) //nolint:staticcheck
		for _, v := range vars {
			if v.Kind == k {
				fmt.Fprintf(out, "  %s\n", v.Key)
			}
		}
	}
}

func printPlan(out io.Writer, target string, p domain.Plan, prune bool) {
	fmt.Fprintf(out, "%s\n\n", target)
	for _, c := range p.Changes {
		label := c.Action.String()
		if c.Action == domain.ActionRemoteOnly && prune {
			label = "delete"
		}
		fmt.Fprintf(out, "  %s %-32s %-8s %s\n", c.Action.Symbol(), c.Key, c.Kind, label)
	}
}

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

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
