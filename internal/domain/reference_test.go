package domain

import (
	"reflect"
	"testing"
)

func TestMergeReferencesUnionsStagesAndPromotesSecret(t *testing.T) {
	got := MergeReferences([]Reference{
		{Key: "DB", Kind: KindVariable, Provider: "gitlab", Stages: []string{"build"}, Sources: []string{"a.yml"}},
		{Key: "DB", Kind: KindSecret, Provider: "gitlab", Stages: []string{"deploy", "build"}, Sources: []string{"b.yml"}},
		{Key: "DB", Kind: KindVariable, Provider: "gitlab", Environment: "prod", Stages: []string{"deploy"}},
	})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	shared := got[0]
	if shared.Kind != KindSecret || !reflect.DeepEqual(shared.Stages, []string{"build", "deploy"}) ||
		!reflect.DeepEqual(shared.Sources, []string{"a.yml", "b.yml"}) {
		t.Fatalf("shared = %+v", shared)
	}
	if got[1].Environment != "prod" {
		t.Fatalf("order = %+v", got)
	}
}

func TestGroupByEnvironmentMergesProviders(t *testing.T) {
	groups := GroupByEnvironment([]Reference{
		{Key: "B", Provider: "github", Environment: "production", Stages: []string{"deploy"}},
		{Key: "B", Provider: "gitlab", Environment: "production", Stages: []string{"release"}},
		{Key: "A", Provider: "github"},
	})
	if len(groups) != 2 || groups[0].Environment != "" || groups[1].Environment != "production" {
		t.Fatalf("groups = %+v", groups)
	}
	b := groups[1].Refs[0]
	if len(groups[1].Refs) != 1 || b.Provider != "" || !reflect.DeepEqual(b.Stages, []string{"deploy", "release"}) {
		t.Fatalf("b = %+v", b)
	}
}

func TestMissingFrom(t *testing.T) {
	got := MissingFrom([]Reference{{Key: "A"}, {Key: "B"}}, []Variable{{Key: "A"}})
	if len(got) != 1 || got[0].Key != "B" {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeCollapsesBranchesToAll(t *testing.T) {
	got := MergeReferences([]Reference{
		{Key: "A", Branches: []string{"main"}},
		{Key: "A", Branches: []string{"develop"}},
		{Key: "B", Branches: []string{"main"}},
		{Key: "B", Branches: []string{BranchAll}},
	})
	if !reflect.DeepEqual(got[0].Branches, []string{"develop", "main"}) || !reflect.DeepEqual(got[1].Branches, []string{BranchAll}) {
		t.Fatalf("got %+v", got)
	}
}

func TestGroupByBranch(t *testing.T) {
	groups := GroupByBranch([]Reference{
		{Key: "DB", Environment: "production", Branches: []string{"main", "release/*"}, Stages: []string{"deploy"}},
		{Key: "DB", Environment: "staging", Branches: []string{"develop"}, Stages: []string{"deploy"}},
		{Key: "LINT", Branches: []string{BranchAll}},
		{Key: "OLD"}, // no branch info = everywhere
	})
	var names []string
	for _, g := range groups {
		names = append(names, g.Environment)
	}
	if !reflect.DeepEqual(names, []string{BranchAll, "develop", "main", "release/*"}) {
		t.Fatalf("names = %v", names)
	}
	if len(groups[0].Refs) != 2 || len(groups[2].Refs) != 1 || groups[2].Refs[0].Key != "DB" {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestMissingRemote(t *testing.T) {
	refs := []Reference{
		{Key: "SHARED", Environment: ""},
		{Key: "DB_URL", Environment: "production"},
		{Key: "STG_ONLY", Environment: "staging"},
		{Key: "API_KEY", Environment: "production"},
	}
	remote := []Remote{{Key: "API_KEY"}}

	got := MissingRemote(refs, "production", remote)
	if len(got) != 2 || got[0].Key != "SHARED" || got[1].Key != "DB_URL" {
		t.Fatalf("production: %+v", got)
	}
	// Repository level needs only shared keys.
	if got := MissingRemote(refs, "", []Remote{{Key: "SHARED"}}); len(got) != 0 {
		t.Fatalf("shared: %+v", got)
	}
}
