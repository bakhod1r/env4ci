package main

import (
	"fmt"
	"strings"
)

// helpFlag is one flag as "env4ci <command> --help" and the man page show it.
type helpFlag struct {
	names, help string
}

var helpFlags = map[string]helpFlag{
	"config":      {"-c, --config <file>", "config file (default env4ci.yaml)"},
	"file":        {"-f, --file <file>", "local .env file (overrides the config)"},
	"env":         {"-e, --env <name>", "GitHub environment / GitLab environment scope"},
	"shared":      {"--shared", `repository level / scope "*" (ignore branches:)`},
	"repo":        {"--repo <owner/name>", "GitHub owner/name or GitLab project path"},
	"all":         {"--all", "every environment in env4ci.yaml, one after another"},
	"allow-empty": {"--allow-empty", "include keys with empty values (skipped by default)"},
	"no-verify":   {"--no-verify", "skip SSH/registry login checks"},
	"prune":       {"--prune", "delete remote keys missing locally"},
	"yes":         {"-y, --yes", "skip confirmation"},
	"out":         {"-o, --out <file>", "output file (default .env.<env> or .env.pulled)"},
	"write":       {"--write", "write .env.example and .env.<group>.example (skips existing)"},
	"by":          {"--by <env|branch>", "group by environment (default) or branch"},
	"dir":         {"--dir <dir>", "project root holding the CI files"},
	"generate":    {"--generate", "random secret, or a new ed25519 key for SSH key variables"},
	"exit-code":   {"--exit-code", "exit 2 when there are changes (for CI drift checks)"},
	"staged":      {"--staged", "only what the next commit adds"},
	"check":       {"--check", "report a newer release without installing it"},
	"targets":     {"--targets <list>", "github, gitlab, vault (comma-separated); no prompts"},
	"envs":        {"--envs <list>", "environments (comma-separated) instead of the ones CI deploys to"},
}

// helpCmd is one subcommand's help.
type helpCmd struct {
	name, args, summary string
	details             string
	flags               []string // keys of helpFlags
}

const providerArg = "[github|gitlab|vault]"

var helpCmds = []helpCmd{
	{"init", "", "create env4ci.yaml, the auth file and one .env file per environment",
		"Asks for targets and repository on a terminal. Environments are the ones the CI files deploy to, else the checked-out branch decides.",
		[]string{"config", "targets", "envs", "repo"}},
	{"gen", "", "create or complete the auth file and the .env file of every environment",
		"Keys the CI files use are added with empty values; existing lines are never changed. An environment CI deploys to that env4ci.yaml lacks is added to it.",
		[]string{"config", "dir"}},
	{"validate", "", "parse and classify the local file, apply validate: rules",
		"", []string{"config", "file", "env"}},
	{"diff", providerArg, "show the plan (never prints values)",
		"Provider and repository default to git remote origin. Without -e, a terminal asks which environment of env4ci.yaml to use; in CI the current branch mapped through branches: decides.",
		[]string{"config", "file", "env", "shared", "repo", "all", "allow-empty", "prune", "exit-code"}},
	{"push", providerArg, "apply the plan after confirmation",
		"SSH keys and registry tokens being written are tried first; nothing is written when a login fails.",
		[]string{"config", "file", "env", "shared", "repo", "all", "allow-empty", "prune", "no-verify", "yes"}},
	{"pull", providerArg, "write readable remote values to a .env file",
		"", []string{"config", "env", "shared", "repo", "out"}},
	{"rotate", "KEY [provider]", "replace one key's value and push only that key",
		"The new value is typed hidden, piped with -y, or made with --generate. It is verified and the env file is backed up first.",
		[]string{"config", "file", "env", "shared", "repo", "generate", "no-verify", "yes"}},
	{"check", providerArg, "exit 2 if CI files use keys the provider lacks",
		"", []string{"config", "env", "shared", "repo", "all", "dir"}},
	{"verify", "", "log in with the SSH keys and registry tokens of the .env file",
		"", []string{"config", "file", "env", "shared"}},
	{"scan", "", "list the variables CI files expect",
		"", []string{"config", "file", "dir", "by", "write"}},
	{"leaks", "", "exit 2 if tracked files contain secret values from local env files",
		"", []string{"config", "file", "staged"}},
	{"hook", "", `install a git pre-commit hook running "env4ci leaks --staged"`,
		"", []string{"config"}},
	{"makefile", "", "add or refresh the env4ci targets in ./Makefile",
		"", []string{"config"}},
	{"update", "", "replace this binary with the latest release (checksum verified)",
		"A Homebrew or Scoop install is left to its package manager.",
		[]string{"check"}},
	{"man", "", "print the manual page (roff)",
		"Install it with: env4ci man > /usr/local/share/man/man1/env4ci.1", nil},
	{"completion", "bash|zsh|fish", "print a tab-completion script for the shell",
		"zsh: source <(env4ci completion zsh) after compinit. bash: source <(env4ci completion bash). fish: env4ci completion fish | source. install.sh installs them where it can.", nil},
	{"help", "[command]", "show this help, or the help of one command", "", nil},
	{"version", "", "print the version", "", nil},
}

// helpAliases are command names that share another command's help.
var helpAliases = map[string]string{"plan": "diff", "apply": "push"}

func findHelp(name string) (helpCmd, bool) {
	if a, ok := helpAliases[name]; ok {
		name = a
	}
	for _, c := range helpCmds {
		if c.name == name {
			return c, true
		}
	}
	return helpCmd{}, false
}

// commandHelp is what "env4ci <name> --help" prints.
func commandHelp(name string) (string, error) {
	c, ok := findHelp(name)
	if !ok {
		return "", fmt.Errorf("unknown command %q\n\n%s", name, usage)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s\n\n  %s\n", strings.TrimSpace("env4ci "+c.name+" "+c.args), c.summary)
	if c.details != "" {
		fmt.Fprintf(&b, "\n%s\n", c.details)
	}
	if len(c.flags) > 0 {
		b.WriteString("\nFlags:\n")
		for _, f := range c.flags {
			fmt.Fprintf(&b, "  %-20s %s\n", helpFlags[f].names, helpFlags[f].help)
		}
	}
	b.WriteString("\nAll commands: env4ci help\n")
	return b.String(), nil
}

// wantsHelp reports a -h / --help among a subcommand's arguments.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			return true
		}
	}
	return false
}

// roff escapes s for a man page: hyphens and backslashes are literal, and
// a leading dot or quote is not a request.
func roff(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	s = strings.ReplaceAll(s, "-", `\-`)
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}

// manPage is env4ci(1) in roff, built from the same tables as --help.
func manPage() string {
	var b strings.Builder
	fmt.Fprintf(&b, ".TH ENV4CI 1 \"\" \"env4ci %s\" \"User Commands\"\n", roff(version))
	b.WriteString(".SH NAME\nenv4ci \\- sync environment variables and secrets across CI/CD platforms\n")
	b.WriteString(".SH SYNOPSIS\n.B env4ci\n.I command\n[\\fIprovider\\fR] [\\fIflags\\fR]\n")
	b.WriteString(".SH DESCRIPTION\n" + roff("env4ci keeps local .env files and the variables and secrets of GitHub Actions, GitLab CI/CD and HashiCorp Vault in sync. Values are never printed.") + "\n")
	b.WriteString(".SH COMMANDS\n")
	for _, c := range helpCmds {
		fmt.Fprintf(&b, ".TP\n%s\n%s.\n", strings.TrimSpace(`\fB`+roff(c.name)+`\fR `+roff(c.args)), roff(c.summary))
		if c.details != "" {
			b.WriteString(roff(c.details) + "\n")
		}
		if len(c.flags) > 0 {
			names := make([]string, 0, len(c.flags))
			for _, f := range c.flags {
				names = append(names, strings.Fields(strings.ReplaceAll(helpFlags[f].names, ",", ""))[0])
			}
			b.WriteString("Flags: " + roff(strings.Join(names, ", ")) + ".\n")
		}
	}
	b.WriteString(".SH OPTIONS\nFlags go after the command.\n")
	seen := map[string]bool{}
	for _, c := range helpCmds {
		for _, f := range c.flags {
			if seen[f] {
				continue
			}
			seen[f] = true
			fmt.Fprintf(&b, ".TP\n\\fB%s\\fR\n%s.\n", roff(helpFlags[f].names), roff(helpFlags[f].help))
		}
	}
	b.WriteString(".SH ENVIRONMENT\n")
	for _, e := range [][2]string{
		{"GITHUB_TOKEN, GH_TOKEN", `GitHub token (falls back to "gh auth token")`},
		{"GITLAB_TOKEN", "GitLab token"},
		{"GITLAB_URL, CI_SERVER_URL", "GitLab address when targets.gitlab.base_url is not set (default https://gitlab.com)"},
		{"VAULT_TOKEN", "Vault token (falls back to ~/.vault-token)"},
		{"VAULT_ADDR", "Vault address"},
	} {
		fmt.Fprintf(&b, ".TP\n\\fB%s\\fR\n%s.\n", roff(e[0]), roff(e[1]))
	}
	b.WriteString(".SH FILES\n.TP\n\\fBenv4ci.yaml\\fR\n" + roff("Configuration: targets, environments, branch mapping, classification and validation rules. Holds no secrets; commit it.") + "\n")
	b.WriteString(".SH EXIT STATUS\n0 ok, 1 error, 2 diff \\-\\-exit\\-code found changes, check found missing keys, or leaks found values.\n")
	b.WriteString(".SH SEE ALSO\nhttps://github.com/bakhod1r/env4ci\n")
	return b.String()
}
