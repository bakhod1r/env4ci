package application

import (
	"context"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

type fakeProvider struct {
	store map[string]domain.Remote
	ops   []string
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) List(context.Context) ([]domain.Remote, error) {
	var out []domain.Remote
	for _, r := range f.store {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeProvider) Set(_ context.Context, v domain.Variable) error {
	f.ops = append(f.ops, "set "+v.Key+" "+v.Kind.String())
	f.store[v.Key] = domain.Remote{Key: v.Key, Value: v.Value, Kind: v.Kind, Known: v.Kind == domain.KindVariable}
	return nil
}
func (f *fakeProvider) Delete(_ context.Context, key string, k domain.Kind) error {
	f.ops = append(f.ops, "delete "+key+" "+k.String())
	return nil
}

func run(t *testing.T, f *fakeProvider, local []domain.Variable, opt ApplyOptions) Result {
	t.Helper()
	s := Service{Provider: f}
	ctx := context.Background()
	remote, _ := f.List(ctx)
	plan, err := s.Plan(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Apply(ctx, local, plan, remote, opt)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestApplyWithoutPruneKeepsRemoteOnly(t *testing.T) {
	f := &fakeProvider{store: map[string]domain.Remote{
		"OLD": {Key: "OLD", Kind: domain.KindVariable, Value: "x", Known: true},
		"A":   {Key: "A", Kind: domain.KindVariable, Value: "1", Known: true},
	}}
	res := run(t, f, []domain.Variable{{Key: "A", Value: "1"}, {Key: "N", Value: "n"}}, ApplyOptions{})
	if res.Written != 1 || res.Deleted != 0 {
		t.Fatalf("res = %+v, ops = %v", res, f.ops)
	}
}

func TestApplyPrune(t *testing.T) {
	f := &fakeProvider{store: map[string]domain.Remote{"OLD": {Key: "OLD", Known: true}}}
	res := run(t, f, nil, ApplyOptions{Prune: true})
	if res.Deleted != 1 || f.ops[0] != "delete OLD variable" {
		t.Fatalf("res = %+v, ops = %v", res, f.ops)
	}
}

func TestApplyKindChangeRemovesOldStore(t *testing.T) {
	f := &fakeProvider{store: map[string]domain.Remote{"A": {Key: "A", Value: "1", Kind: domain.KindVariable, Known: true}}}
	run(t, f, []domain.Variable{{Key: "A", Value: "1", Kind: domain.KindSecret}}, ApplyOptions{})
	if len(f.ops) != 2 || f.ops[0] != "set A secret" || f.ops[1] != "delete A variable" {
		t.Fatalf("ops = %v", f.ops)
	}
}

func TestPlanRejectsInvalidKey(t *testing.T) {
	s := Service{Provider: &fakeProvider{store: map[string]domain.Remote{}}}
	if _, err := s.Plan(context.Background(), []domain.Variable{{Key: "BAD-KEY"}}); err == nil {
		t.Fatal("want error")
	}
}
