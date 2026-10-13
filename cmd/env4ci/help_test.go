package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestCommandHelp(t *testing.T) {
	for _, args := range [][]string{{"help", "push"}, {"push", "--help"}, {"push", "-h"}, {"push", "github", "-help"}, {"apply", "--help"}} {
		s := mustRun(t, args...)
		for _, want := range []string{"Usage: env4ci push [github|gitlab|vault]", "apply the plan after confirmation", "--prune", "-y, --yes", "All commands: env4ci help"} {
			if !strings.Contains(s, want) {
				t.Errorf("%v: missing %q in\n%s", args, want, s)
			}
		}
		if strings.Contains(s, "--generate") {
			t.Errorf("%v: shows a flag push does not take:\n%s", args, s)
		}
	}
	// No flags, no details: just the usage line.
	if s := mustRun(t, "version", "--help"); !strings.Contains(s, "Usage: env4ci version\n") || strings.Contains(s, "Flags:") {
		t.Fatal(s)
	}
	// Needs no env4ci.yaml and no network.
	if s := mustRun(t, "update", "--help"); !strings.Contains(s, "--check") {
		t.Fatal(s)
	}
	if s := mustRun(t, "help"); !strings.Contains(s, "Usage:") || !strings.Contains(s, "env4ci help [command]") {
		t.Fatal(s)
	}
	runErr(t, `unknown command "nope"`, "help", "nope")
	runErr(t, `unknown command "nope"`, "nope", "--help")
}

// The usage text, the help tables and the flag parser must name the same
// commands and flags.
func TestHelpTablesMatchUsageAndFlags(t *testing.T) {
	inUsage := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^  env4ci (\w+)`).FindAllStringSubmatch(usage, -1) {
		inUsage[m[1]] = true
	}
	inTable := map[string]bool{}
	for _, c := range helpCmds {
		inTable[c.name] = true
		if !inUsage[c.name] {
			t.Errorf("command %s has help but is not in the usage text", c.name)
		}
		if c.summary == "" {
			t.Errorf("command %s has no summary", c.name)
		}
		for _, f := range c.flags {
			h, ok := helpFlags[f]
			if !ok {
				t.Errorf("command %s: unknown flag %q", c.name, f)
				continue
			}
			// Every listed flag parses.
			arg := "--" + f
			if strings.Contains(h.names, "<") {
				arg += "=x"
			}
			if _, _, err := parseFlags([]string{arg}); err != nil {
				t.Errorf("flag %s: %v", arg, err)
			}
		}
	}
	for name := range inUsage {
		if !inTable[name] {
			t.Errorf("command %s is in the usage text but has no help", name)
		}
	}
	for alias, target := range helpAliases {
		if !inTable[target] {
			t.Errorf("alias %s points to unknown command %s", alias, target)
		}
	}
	used := map[string]bool{}
	for _, c := range helpCmds {
		for _, f := range c.flags {
			used[f] = true
		}
	}
	for f := range helpFlags {
		if !used[f] {
			t.Errorf("flag %s is documented but no command lists it", f)
		}
	}
}

func TestManPage(t *testing.T) {
	old := version
	version = "0.4.0-test"
	defer func() { version = old }()
	s := mustRun(t, "man")
	for _, want := range []string{
		`.TH ENV4CI 1 "" "env4ci 0.4.0\-test" "User Commands"`,
		".SH NAME\nenv4ci \\- sync",
		".SH COMMANDS\n", "\\fBpush\\fR [github|gitlab|vault]\n", "\\fBupdate\\fR\n",
		".SH OPTIONS\n", `\fB\-c, \-\-config <file>\fR`, `\fB\-\-check\fR`,
		".SH ENVIRONMENT\n", `\fBVAULT_TOKEN\fR`, ".SH EXIT STATUS\n", ".SH SEE ALSO\n",
		"Flags: \\-c, \\-\\-dir.\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("man page lacks %q", want)
		}
	}
	// No unescaped hyphen-minus flags, and each option is listed once.
	if regexp.MustCompile(`(^|[^\\])--`).MatchString(s) {
		t.Error("man page has an unescaped --")
	}
	if n := strings.Count(s, `\fB\-c, \-\-config <file>\fR`); n != 1 {
		t.Errorf("--config listed %d times in OPTIONS", n)
	}
	for in, want := range map[string]string{`a\b`: `a\eb`, ".env": `\&.env`, "'q": `\&'q`, "x-y": `x\-y`} {
		if got := roff(in); got != want {
			t.Errorf("roff(%q) = %q, want %q", in, got, want)
		}
	}
}
