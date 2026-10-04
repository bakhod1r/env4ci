package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/ciscan"
	"github.com/bakhod1r/env4ci/internal/infrastructure/config"
)

// cmdCheck reports keys the CI files use that the provider does not hold.
// An environment target also sees repository-level (shared) keys, so both
// levels are listed. Values are never read into output.
func cmdCheck(ctx context.Context, cfg config.Config, o opts, t target, p application.Provider, out io.Writer) error {
	cl, err := cfg.Classifier()
	if err != nil {
		return err
	}
	refs, err := ciscan.Scan(first(o.dir, "."), cl)
	if err != nil {
		return err
	}
	if t.Provider != "vault" { // Vault serves any CI; others only their own files
		var own []domain.Reference
		for _, r := range refs {
			if r.Provider == t.Provider {
				own = append(own, r)
			}
		}
		refs = own
	}
	remote, err := p.List(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", p.Name(), err)
	}
	if t.Environment != "" {
		st := t
		st.Environment = ""
		if st.Vault != nil {
			st.Repo = vaultPath(first(o.repo, st.Vault.Path), "")
		}
		sp, _ := newProvider(st) // same provider and token as t, which succeeded
		shared, err := sp.List(ctx)
		if err != nil {
			return fmt.Errorf("%s (shared): %w", sp.Name(), err)
		}
		remote = append(remote, shared...)
	}
	var merged []domain.Reference // one line per key even when GitHub and GitLab both use it
	for _, g := range domain.GroupByEnvironment(refs) {
		merged = append(merged, g.Refs...)
	}
	missing := domain.MissingRemote(merged, t.Environment, remote)
	pal := newPalette(out)
	if len(missing) == 0 {
		fmt.Fprintf(out, "%s %s has every key CI uses\n", pal.Green("✓"), p.Name())
		return nil
	}
	fmt.Fprintf(out, "%s missing in %s:\n", pal.Red("✗"), p.Name())
	for _, m := range missing {
		fmt.Fprintf(out, "  %-28s %-8s %s  (%s)\n", m.Key, m.Kind, envLabel(m.Environment), strings.Join(m.Sources, ", "))
	}
	return errDrift
}
