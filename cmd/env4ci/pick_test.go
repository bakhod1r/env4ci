package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func asTerminal(t *testing.T) {
	t.Helper()
	old := terminal
	terminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { terminal = old })
}

func TestPushAsksForEnvironment(t *testing.T) {
	asTerminal(t)
	for answer, env := range map[string]string{"3\ny\n": "staging", "production\ny\n": "production"} {
		_, cfg, store := vaultProject(t, map[string]string{"production": "A=1\n", "staging": "A=2\n", "dev": "A=3\n"})
		var out bytes.Buffer
		if err := run(context.Background(), []string{"push", "-c", cfg}, strings.NewReader(answer), &out); err != nil {
			t.Fatalf("%q: %v\n%s", answer, err, out.String())
		}
		for _, want := range []string{"Environments", "1) dev", "2) production", "3) staging", "Environment [1-3 or name]: "} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%q: missing %q\n%s", answer, want, out.String())
			}
		}
		if len(store) != 1 || store["app/"+env] == nil {
			t.Errorf("%q: wrote %v, want only app/%s", answer, keysOf(store), env)
		}
	}
}

func keysOf(m map[string]map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPickEnvironmentEdges(t *testing.T) {
	asTerminal(t)
	_, cfg, _ := vaultProject(t, map[string]string{"production": "A=1\n", "staging": "A=2\n"})
	for _, answer := range []string{"9\n", "qa\n", ""} {
		var out bytes.Buffer
		err := run(context.Background(), []string{"diff", "-c", cfg}, strings.NewReader(answer), &out)
		if err == nil || !strings.Contains(err.Error(), "want 1-2 or one of production, staging") {
			t.Errorf("%q: err = %v", answer, err)
		}
	}
	// One environment: used without a question.
	_, cfg, store := vaultProject(t, map[string]string{"production": "A=1\n"})
	var out bytes.Buffer
	if err := run(context.Background(), []string{"push", "-c", cfg, "-y"}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "Environment [") || store["app/production"] == nil {
		t.Fatalf("%s", out.String())
	}
	// -e skips the question.
	_, cfg, store = vaultProject(t, map[string]string{"production": "A=1\n", "staging": "A=2\n"})
	out.Reset()
	if err := run(context.Background(), []string{"push", "-c", cfg, "-e", "staging", "-y"}, strings.NewReader(""), &out); err != nil || store["app/staging"] == nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}
