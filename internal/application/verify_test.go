package application

import (
	"context"
	"errors"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func TestRunChecksSelectsTouchedAndKeepsOrder(t *testing.T) {
	ran := map[string]bool{}
	mk := func(name string, err error, keys ...string) Check {
		return Check{Name: name, Keys: keys, Run: func(context.Context) error { return err }}
	}
	checks := []Check{
		mk("ssh", errors.New("auth failed"), "SSH_KEY", "SSH_HOST"),
		mk("ghcr", nil, "GHCR_TOKEN"),
		mk("hub", nil, "DOCKERHUB_TOKEN"),
	}
	plan := domain.Plan{Changes: []domain.Change{
		{Key: "SSH_HOST", Action: domain.ActionUpdate},
		{Key: "GHCR_TOKEN", Action: domain.ActionUnverifiable},
		{Key: "DOCKERHUB_TOKEN", Action: domain.ActionNoop},
	}}
	rs := RunChecks(context.Background(), checks, TouchedKeys(plan))
	for _, r := range rs {
		ran[r.Name] = true
	}
	if len(rs) != 2 || rs[0].Name != "ssh" || rs[1].Name != "ghcr" || ran["hub"] {
		t.Fatalf("results = %+v", rs)
	}
	if !AnyFailed(rs) {
		t.Fatal("want failure")
	}
	if all := RunChecks(context.Background(), checks, nil); len(all) != 3 {
		t.Fatalf("nil touched should run all, got %d", len(all))
	}
}
