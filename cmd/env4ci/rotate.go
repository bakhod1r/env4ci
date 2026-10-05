package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
	"github.com/bakhod1r/env4ci/internal/infrastructure/dotenv"
	"github.com/bakhod1r/env4ci/internal/infrastructure/keygen"
)

// Terminal seams; tests replace them.
var (
	stdinIsTerminal = isTerminal
	readHidden      = func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) } // #nosec G115 -- fd fits int
)

// cmdRotate replaces one key's value: new value (generated, typed or piped),
// value rules, credential check, backup of the old file, local write, then a
// push of that key only. The value is never printed; a generated SSH key
// prints its public half for the server's authorized_keys.
func cmdRotate(ctx context.Context, cfg config.Config, o opts, pos []string, in io.Reader, out io.Writer) error {
	if len(pos) == 0 || len(pos) > 2 {
		return errors.New("usage: env4ci rotate KEY [github|gitlab|vault] [-e env] [--generate]")
	}
	key, pos := pos[0], pos[1:]
	t, err := resolveTarget(cfg, o, pos, gitSource{dir: "."})
	if err != nil {
		return err
	}
	if filepath.Clean(t.File) == filepath.Clean(cfg.AuthPath()) {
		return fmt.Errorf("refusing to rotate in %s: it holds env4ci's own tokens", t.File)
	}
	o.env, o.file = t.Environment, t.File
	pal := newPalette(out)
	fmt.Fprintln(out, pal.Dim(t.Describe()))

	content, err := os.ReadFile(t.File)
	if err != nil {
		return err
	}
	local, err := loadLocal(cfg, o)
	if err != nil {
		return err
	}
	idx := -1
	for i, v := range local {
		if v.Key == key {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("%s is not in %s", key, t.File)
	}
	old := local[idx].Value

	br := bufio.NewReader(in) // one reader: value and confirmations share it
	newVal, pub, err := newValue(key, old, o, br, out)
	if err != nil {
		return err
	}
	if newVal == old {
		return fmt.Errorf("new value of %s equals the current one", key)
	}
	local[idx].Value = newVal
	if err := checkValues(cfg, local, t.Environment, out); err != nil {
		return err
	}
	if pub != "" {
		fmt.Fprintf(out, "\nNew public key for %s. Add it to the server's ~/.ssh/authorized_keys\n(remove the old key after the push):\n\n  %s\n\n", key, pub)
	}
	if !o.noVerify {
		if pub != "" && !o.yes && !confirm(br, out, "Public key installed? Verify login and continue [y/N] ") {
			return errors.New("aborted")
		}
		if err := runCredentialChecks(ctx, out, credentialChecks(cfg, local), map[string]bool{key: true}); err != nil {
			return err
		}
	}

	p, err := newProvider(t)
	if err != nil {
		return err
	}
	svc := application.Service{Provider: p}
	one := []domain.Variable{local[idx]}
	remote, err := p.List(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", p.Name(), err)
	}
	plan, err := svc.Plan(ctx, one)
	if err != nil {
		return err
	}
	plan = onlyKey(plan, key)
	printPlan(out, p.Name(), plan, false)
	if !o.yes && !confirm(br, out, "\nWrite the new value locally and to "+p.Name()+"? [y/N] ") {
		return errors.New("aborted")
	}

	backup := t.File + ".bak"
	if err := protect(".", backup, out, pal); err != nil {
		return err
	}
	if err := os.WriteFile(backup, content, 0o600); err != nil { // #nosec G703 -- env file path from env4ci.yaml / -f
		return err
	}
	if err := os.WriteFile(t.File, []byte(dotenv.Set(string(content), key, newVal)), 0o600); err != nil { // #nosec G703 -- env file path from env4ci.yaml / -f
		return err
	}
	fmt.Fprintf(out, "%s %s updated (old file kept in %s)\n", pal.Green("✓"), t.File, backup)

	res, err := svc.Apply(ctx, one, plan, remote, application.ApplyOptions{})
	writeAudit(cfg, t, plan, false, err, out)
	if err != nil {
		return fmt.Errorf("%w (local file already has the new value; old one is in %s)", err, backup)
	}
	fmt.Fprintf(out, "%s %s rotated: %d written to %s\n", pal.Green("✓"), key, res.Written, p.Name())
	return nil
}

// newValue picks the replacement: --generate (SSH key when the key holds
// one, else a random secret), a hidden prompt on a terminal, or stdin.
func newValue(key, old string, o opts, br *bufio.Reader, out io.Writer) (val, pub string, err error) {
	switch {
	case o.generate && (domain.LooksLikePrivateKey(old) || domain.KnownHostsFor(key) != ""):
		val, pub = keygen.SSH("env4ci " + key)
		return val, pub, nil
	case o.generate:
		return keygen.Secret(), "", nil
	case stdinIsTerminal(os.Stdin) && br.Buffered() == 0:
		fmt.Fprintf(out, "New value for %s (hidden): ", key)
		b, err := readHidden()
		fmt.Fprintln(out)
		if err != nil {
			return "", "", err
		}
		val = strings.TrimSpace(string(b))
	default:
		if !o.yes {
			return "", "", errors.New("rotate: a piped value needs -y (stdin cannot also answer prompts)")
		}
		b, err := io.ReadAll(br)
		if err != nil {
			return "", "", err
		}
		val = strings.TrimRight(string(b), "\r\n")
	}
	if val == "" {
		return "", "", errors.New("rotate: empty value (type it, pipe it, or use --generate)")
	}
	return val, "", nil
}

func onlyKey(p domain.Plan, key string) domain.Plan {
	var out domain.Plan
	for _, c := range p.Changes {
		if c.Key == key {
			out.Changes = append(out.Changes, c)
		}
	}
	return out
}
