package main

import (
	"fmt"
	"strings"
)

var completionShells = []string{"bash", "zsh", "fish"}

// flagParts splits a helpFlag's names: "-c, --config <file>" gives
// short "c", long "config", arg "file". arg is "" for a switch.
func flagParts(names string) (short, long, arg string) {
	for _, f := range strings.Fields(strings.ReplaceAll(names, ",", "")) {
		switch {
		case strings.HasPrefix(f, "--"):
			long = f[2:]
		case strings.HasPrefix(f, "-"):
			short = f[1:]
		default:
			arg = strings.Trim(f, "<>")
		}
	}
	return short, long, arg
}

// positionals are the words a command takes besides flags.
func positionals(c helpCmd) []string {
	switch {
	case strings.Contains(c.args, "provider") || c.args == providerArg:
		return []string{"github", "gitlab", "vault"}
	case c.name == "completion":
		return completionShells
	case c.name == "help":
		names := make([]string, 0, len(helpCmds))
		for _, h := range helpCmds {
			names = append(names, h.name)
		}
		return names
	}
	return nil
}

// completionScript is the tab-completion script for shell, built from the
// same tables as --help.
func completionScript(shell string) (string, error) {
	switch shell {
	case "bash":
		return bashCompletion(), nil
	case "zsh":
		return zshCompletion(), nil
	case "fish":
		return fishCompletion(), nil
	}
	return "", fmt.Errorf("completion: want one of %s, got %q", strings.Join(completionShells, ", "), shell)
}

func bashCompletion() string {
	var names []string
	var cases strings.Builder
	for _, c := range helpCmds {
		names = append(names, c.name)
		var flags []string
		for _, f := range c.flags {
			short, long, _ := flagParts(helpFlags[f].names)
			if short != "" {
				flags = append(flags, "-"+short)
			}
			flags = append(flags, "--"+long)
		}
		fmt.Fprintf(&cases, "\t%s) flags=%q; args=%q ;;\n", c.name, strings.Join(append(flags, "--help"), " "), strings.Join(positionals(c), " "))
	}
	return `# bash completion for env4ci. Load it with:
#   source <(env4ci completion bash)
_env4ci() {
	local cur=${COMP_WORDS[COMP_CWORD]} flags="" args=""
	if [ "$COMP_CWORD" -eq 1 ]; then
		COMPREPLY=($(compgen -W "` + strings.Join(names, " ") + `" -- "$cur"))
		return
	fi
	case "${COMP_WORDS[1]}" in
` + cases.String() + `	esac
	case "$cur" in
	-*) COMPREPLY=($(compgen -W "$flags" -- "$cur")) ;;
	*) COMPREPLY=($(compgen -W "$args" -- "$cur")) ;;
	esac
}
complete -o default -F _env4ci env4ci
`
}

// zshQuote makes s safe inside a single-quoted _arguments / _describe spec.
func zshQuote(s string) string {
	r := strings.NewReplacer(`'`, `'\''`, `[`, `\[`, `]`, `\]`, `:`, `\:`)
	return r.Replace(s)
}

// zshAction completes a flag's value.
func zshAction(arg string) string {
	switch {
	case arg == "file":
		return ":file:_files"
	case arg == "dir":
		return ":directory:_files -/"
	case strings.Contains(arg, "|"):
		return ":value:(" + strings.ReplaceAll(arg, "|", " ") + ")"
	}
	return ":" + arg + ":"
}

func zshCompletion() string {
	var describe, cases strings.Builder
	for _, c := range helpCmds {
		fmt.Fprintf(&describe, "\t\t'%s:%s'\n", c.name, zshQuote(c.summary))
		fmt.Fprintf(&cases, "\t%s)\n\t\t_arguments -s \\\n", c.name)
		for _, f := range c.flags {
			short, long, arg := flagParts(helpFlags[f].names)
			action := ""
			if arg != "" {
				action = zshAction(arg)
			}
			help := zshQuote(helpFlags[f].help)
			if short != "" {
				fmt.Fprintf(&cases, "\t\t\t'(-%s --%s)'{-%s,--%s}'[%s]%s' \\\n", short, long, short, long, help, action)
			} else {
				fmt.Fprintf(&cases, "\t\t\t'--%s[%s]%s' \\\n", long, help, action)
			}
		}
		if words := positionals(c); len(words) > 0 {
			fmt.Fprintf(&cases, "\t\t\t'*:argument:(%s)' \\\n", strings.Join(words, " "))
		}
		cases.WriteString("\t\t\t'--help[show help for this command]'\n\t\t;;\n")
	}
	return `#compdef env4ci
# zsh completion for env4ci. Load it with:
#   source <(env4ci completion zsh)      (after compinit, e.g. in ~/.zshrc)
# or save it as _env4ci in a directory of $fpath.
compdef _env4ci env4ci

_env4ci() {
	local -a commands
	commands=(
` + describe.String() + `	)
	if (( CURRENT == 2 )); then
		_describe -t commands 'env4ci command' commands
		return
	fi
	local cmd=${words[2]}
	shift words
	(( CURRENT-- ))
	case $cmd in
` + cases.String() + `	esac
}

if [ "$funcstack[1]" = "_env4ci" ]; then
	_env4ci "$@"
fi
`
}

// fishQuote makes s safe inside single quotes.
func fishQuote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# fish completion for env4ci. Load it with:\n#   env4ci completion fish | source\n")
	for _, c := range helpCmds {
		fmt.Fprintf(&b, "complete -c env4ci -f -n __fish_use_subcommand -a %s -d '%s'\n", c.name, fishQuote(c.summary))
	}
	for _, c := range helpCmds {
		seen := "'__fish_seen_subcommand_from " + c.name + "'"
		for _, f := range c.flags {
			short, long, arg := flagParts(helpFlags[f].names)
			fmt.Fprintf(&b, "complete -c env4ci -n %s", seen)
			if short != "" {
				fmt.Fprintf(&b, " -s %s", short)
			}
			fmt.Fprintf(&b, " -l %s", long)
			if arg != "" {
				b.WriteString(" -r")
			}
			fmt.Fprintf(&b, " -d '%s'\n", fishQuote(helpFlags[f].help))
		}
		if words := positionals(c); len(words) > 0 {
			fmt.Fprintf(&b, "complete -c env4ci -f -n %s -a '%s'\n", seen, strings.Join(words, " "))
		}
	}
	return b.String()
}
