package domain

import (
	"reflect"
	"testing"
)

func TestAuditChanges(t *testing.T) {
	p := Plan{Changes: []Change{
		{Key: "A", Kind: KindSecret, Action: ActionCreate},
		{Key: "B", Kind: KindVariable, Action: ActionNoop},
		{Key: "C", Kind: KindVariable, Action: ActionUpdate},
		{Key: "D", Kind: KindSecret, Action: ActionUnverifiable},
		{Key: "E", Kind: KindVariable, Action: ActionRemoteOnly},
	}}
	want := []AuditChange{{Key: "A", Kind: "secret", Action: "create"}, {Key: "C", Kind: "variable", Action: "update"}, {Key: "D", Kind: "secret", Action: "update"}}
	if got := AuditChanges(p, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	want = append(want, AuditChange{Key: "E", Kind: "variable", Action: "delete"})
	if got := AuditChanges(p, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("prune: got %+v", got)
	}
}
