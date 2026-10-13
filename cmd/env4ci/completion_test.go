package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlagParts(t *testing.T) {
	for names, want := range map[string][3]string{
		"-c, --config <file>": {"c", "config", "file"},
		"--shared":            {"", "shared", ""},
		"-y, --yes":           {"y", "yes", ""},
		"--by <env|branch>":   {"", "by", "env|branch"},
	} {
		s, l, a := flagParts(names)
		if got := [3]string{s, l, a}; got != want {
			t.Errorf("flagParts(%q) = %v, want %v", names, got, want)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	for shell, wants := range map[string][]string{
		"bash": {
			"complete -o default -F _env4ci env4ci",
			`gen) flags="-c --config --dir --help"; args="" ;;`,
			`push) flags="-c --config -f --file -e --env --shared --repo --all --allow-empty --prune --no-verify -y --yes --help"; args="github gitlab vault" ;;`,
			`completion) flags="--help"; args="bash zsh fish" ;;`,
		},
		"zsh": {
			"#compdef env4ci\n", "compdef _env4ci env4ci",
			`'gen:create or complete the auth file and the .env file of every environment'`,
			`'(-c --config)'{-c,--config}'[config file (default env4ci.yaml)]:file:_files'`,
			`'--dir[project root holding the CI files]:directory:_files -/'`,
			`'--by[group by environment (default) or branch]:value:(env branch)'`,
			`'--shared[repository level / scope "*" (ignore branches\:)]'`,
			`'--repo[GitHub owner/name or GitLab project path]:owner/name:'`,
			`'*:argument:(github gitlab vault)'`,
			`'hook:install a git pre-commit hook running "env4ci leaks --staged"'`,
		},
		"fish": {
			"complete -c env4ci -f -n __fish_use_subcommand -a gen -d 'create or complete",
			"complete -c env4ci -n '__fish_seen_subcommand_from gen' -s c -l config -r -d 'config file (default env4ci.yaml)'",
			"complete -c env4ci -n '__fish_seen_subcommand_from push' -l prune -d 'delete remote keys missing locally'",
			"complete -c env4ci -f -n '__fish_seen_subcommand_from diff' -a 'github gitlab vault'",
		},
	} {
		s := mustRun(t, "completion", shell)
		for _, want := range wants {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", shell, want)
			}
		}
	}
	runErr(t, "want one of bash, zsh, fish", "completion", "tcsh")
	runErr(t, "name one shell", "completion")
	runErr(t, "name one shell", "completion", "zsh", "bash")
	if got := zshQuote(`it's [x]: y`); got != `it'\''s \[x\]\: y` {
		t.Errorf("zshQuote = %q", got)
	}
	if got := fishQuote(`it's a\b`); got != `it\'s a\\b` {
		t.Errorf("fishQuote = %q", got)
	}
	if got := positionals(helpCmd{name: "help"}); len(got) != len(helpCmds) {
		t.Errorf("help completes %v", got)
	}
}

// The scripts must be valid for the shells that are installed here.
func TestCompletionScriptsParse(t *testing.T) {
	for shell, args := range map[string][]string{"bash": {"-n"}, "zsh": {"-n"}, "fish": {"--no-execute"}} {
		bin, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		f := filepath.Join(t.TempDir(), "env4ci."+shell)
		os.WriteFile(f, []byte(mustRun(t, "completion", shell)), 0o644)
		if out, err := exec.Command(bin, append(args, f)...).CombinedOutput(); err != nil {
			t.Errorf("%s rejects its completion script: %v\n%s", shell, err, out)
		}
	}
}
