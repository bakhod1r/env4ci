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
