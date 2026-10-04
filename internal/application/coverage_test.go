package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

var errBoom = errors.New("boom")

// failingProvider fails the operation named in failOn.
type failingProvider struct {
	fakeProvider
	failOn string
}

func (f *failingProvider) List(ctx context.Context) ([]domain.Remote, error) {
	if f.failOn == "list" {
		return nil, errBoom
	}
	return f.fakeProvider.List(ctx)
}
func (f *failingProvider) Set(ctx context.Context, v domain.Variable) error {
	if f.failOn == "set" {
		return errBoom
	}
	return f.fakeProvider.Set(ctx, v)
}
func (f *failingProvider) Delete(ctx context.Context, k string, kind domain.Kind) error {
	if f.failOn == "delete" {
		return errBoom
	}
	return f.fakeProvider.Delete(ctx, k, kind)
}

func TestPlanListError(t *testing.T) {
	_, err := Service{Provider: &failingProvider{failOn: "list"}}.Plan(context.Background(), nil)
	if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "fake: list") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyErrors(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		failOn string
		remote []domain.Remote
		local  []domain.Variable
		prune  bool
		want   string
	}{
		"set fails":          {failOn: "set", local: []domain.Variable{{Key: "A", Value: "1"}}, want: "set A"},
		"kind-change delete": {failOn: "delete", remote: []domain.Remote{{Key: "A", Kind: domain.KindVariable, Known: true}}, local: []domain.Variable{{Key: "A", Kind: domain.KindSecret}}, want: "delete old variable A"},
		"prune delete fails": {failOn: "delete", remote: []domain.Remote{{Key: "OLD", Known: true}}, prune: true, want: "delete OLD"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := map[string]domain.Remote{}
			for _, r := range tc.remote {
				store[r.Key] = r
			}
			p := &failingProvider{fakeProvider: fakeProvider{store: store}, failOn: tc.failOn}
			plan := domain.ComputePlan(tc.local, tc.remote)
			_, err := Service{Provider: p}.Apply(ctx, tc.local, plan, tc.remote, ApplyOptions{Prune: tc.prune})
			if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestPull(t *testing.T) {
	p := &fakeProvider{store: map[string]domain.Remote{
		"V": {Key: "V", Value: "1", Known: true},
		"S": {Key: "S", Kind: domain.KindSecret},
	}}
	known, hidden, err := Service{Provider: p}.Pull(context.Background())
	if err != nil || len(known) != 1 || known[0].Key != "V" || !reflect.DeepEqual(hidden, []string{"S"}) {
		t.Fatalf("known=%v hidden=%v err=%v", known, hidden, err)
	}
	if _, _, err := (Service{Provider: &failingProvider{failOn: "list"}}).Pull(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
}

type failingBatch struct{ batchProvider }

func (*failingBatch) ApplyBatch(context.Context, []domain.Variable, []string) error { return errBoom }

func TestApplyBatchNothingAndError(t *testing.T) {
	ctx := context.Background()
	b := &batchProvider{fakeProvider: fakeProvider{store: map[string]domain.Remote{}}}
	plan := domain.Plan{Changes: []domain.Change{{Key: "X", Action: domain.ActionRemoteOnly}}} // no prune: nothing
	if res, err := (Service{Provider: b}).Apply(ctx, nil, plan, nil, ApplyOptions{}); err != nil || res != (Result{}) || b.set != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	f := &failingBatch{}
	plan = domain.Plan{Changes: []domain.Change{{Key: "A", Action: domain.ActionCreate}}}
	if _, err := (Service{Provider: f}).Apply(ctx, []domain.Variable{{Key: "A"}}, plan, nil, ApplyOptions{}); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
}

func TestAnyFailedFalse(t *testing.T) {
	if AnyFailed([]CheckResult{{Name: "ok"}}) || AnyFailed(nil) {
		t.Fatal("no failures expected")
	}
}

func TestAuthKeys(t *testing.T) {
	var names []string
	for _, k := range AuthKeys([]string{"github", "gitlab", "vault", "unknown"}) {
		names = append(names, k.Name)
		if k.Comment == "" {
			t.Errorf("%s: no comment", k.Name)
		}
	}
	if strings.Join(names, ",") != "GITHUB_TOKEN,GITLAB_TOKEN,GITLAB_URL,VAULT_ADDR,VAULT_TOKEN,VAULT_NAMESPACE" {
		t.Fatalf("%v", names)
	}
	for k, want := range map[string]bool{"GITHUB_TOKEN": true, "GH_TOKEN": true, "VAULT_NAMESPACE": true, "DATABASE_URL": false} {
		if IsAuthKey(k) != want {
			t.Errorf("IsAuthKey(%q) != %v", k, want)
		}
	}
}
