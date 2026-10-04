package application

import (
	"context"
	"errors"
	"strings"
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

type validatingProvider struct{ fakeProvider }

func (validatingProvider) Validate(v domain.Variable) error {
	if v.Value == "" {
		return errors.New(v.Key + " empty")
	}
	return nil
}

func TestPlanRunsProviderValidationForAllKeys(t *testing.T) {
	p := &validatingProvider{fakeProvider{store: map[string]domain.Remote{}}}
	_, err := Service{Provider: p}.Plan(context.Background(), []domain.Variable{{Key: "A"}, {Key: "B", Value: "ok"}, {Key: "C"}})
	if err == nil || !strings.Contains(err.Error(), "A empty") || !strings.Contains(err.Error(), "C empty") {
		t.Fatalf("err = %v", err)
	}
}

type batchProvider struct {
	fakeProvider
	set []domain.Variable
	del []string
}

func (*batchProvider) KindAgnostic() {}
func (b *batchProvider) ApplyBatch(_ context.Context, set []domain.Variable, del []string) error {
	b.set, b.del = set, del
	return nil
}

func TestBatchAndKindAgnostic(t *testing.T) {
	b := &batchProvider{fakeProvider: fakeProvider{store: map[string]domain.Remote{
		"SAME": {Key: "SAME", Value: "1", Kind: domain.KindSecret, Known: true},
		"OLD":  {Key: "OLD", Value: "x", Kind: domain.KindSecret, Known: true},
	}}}
	s := Service{Provider: b}
	ctx := context.Background()
	local := []domain.Variable{
		{Key: "SAME", Value: "1", Kind: domain.KindVariable}, // kind ignored: no change
		{Key: "NEW", Value: "n", Kind: domain.KindVariable},
	}
	remote, _ := b.List(ctx)
	plan, err := s.Plan(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Apply(ctx, local, plan, remote, ApplyOptions{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != 1 || res.Deleted != 1 || len(b.set) != 1 || b.set[0].Key != "NEW" || b.del[0] != "OLD" || len(b.ops) != 0 {
		t.Fatalf("res=%+v set=%+v del=%v ops=%v", res, b.set, b.del, b.ops)
	}
}
