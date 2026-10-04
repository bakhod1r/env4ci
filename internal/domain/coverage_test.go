package domain

import (
	"reflect"
	"testing"
)

func TestKindStringAndParse(t *testing.T) {
	if KindSecret.String() != "secret" || KindVariable.String() != "variable" {
		t.Fatal("String")
	}
	for in, want := range map[string]Kind{"secret": KindSecret, "SECRET": KindSecret, "variable": KindVariable, "var": KindVariable} {
		if got, err := ParseKind(in); err != nil || got != want {
			t.Errorf("ParseKind(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseKind("nope"); err == nil {
		t.Fatal("want error")
	}
}

func TestActionSymbolAndString(t *testing.T) {
	want := map[Action][2]string{
		ActionNoop: {" ", "unchanged"}, ActionCreate: {"+", "new"}, ActionUpdate: {"~", "changed"},
		ActionUnverifiable: {"?", "unverifiable"}, ActionRemoteOnly: {"-", "remote only"},
	}
	for a, w := range want {
		if a.Symbol() != w[0] || a.String() != w[1] {
			t.Errorf("%d: %q %q", a, a.Symbol(), a.String())
		}
	}
}

func TestPlanWriteKeysAndHasWrites(t *testing.T) {
	p := Plan{Changes: []Change{
		{Key: "A", Action: ActionCreate}, {Key: "B", Action: ActionUpdate}, {Key: "C", Action: ActionUnverifiable},
		{Key: "D", Action: ActionNoop}, {Key: "E", Action: ActionRemoteOnly},
	}}
	if !reflect.DeepEqual(p.WriteKeys(), []string{"A", "B", "C"}) || !p.HasWrites() {
		t.Fatalf("%v", p.WriteKeys())
	}
	idle := Plan{Changes: []Change{{Key: "D", Action: ActionNoop}, {Key: "E", Action: ActionRemoteOnly}}}
	if idle.HasWrites() || idle.WriteKeys() != nil {
		t.Fatal("idle plan writes")
	}
}

func TestRegistryCredentialKeys(t *testing.T) {
	if !reflect.DeepEqual(RegistryCredential{Registry: "r", Username: "U", Password: "P"}.Keys(), []string{"P", "U"}) {
		t.Fatal("keys")
	}
	if got := (RegistryCredential{Password: "P"}).Keys(); !reflect.DeepEqual(got, []string{"P"}) {
		t.Fatalf("%v", got)
	}
}

func TestKeyPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"DEPLOY_SSH_PRIVATE_KEY": "DEPLOY", "DEPLOY_PRIVATE_KEY": "DEPLOY", "DEPLOY_SSH_KEY": "DEPLOY", "API_KEY": "API",
		"SSH_KEY": "", "PRIVATE_KEY": "", "SSH_PRIVATE_KEY": "", "CERT": "CERT",
	} {
		if got := keyPrefix(in, "_SSH_PRIVATE_KEY", "_PRIVATE_KEY", "_SSH_KEY", "_KEY"); got != want {
			t.Errorf("keyPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBranchMapEqualLengthGlobsAlphabetical(t *testing.T) {
	m := BranchMap{"a/*": "first", "*/b": "second"} // same length, both match "a/b"
	if env, _ := m.Environment("a/b"); env != "second" {
		t.Fatalf("got %q (want alphabetical: \"*/b\" < \"a/*\")", env)
	}
}

func TestClassifyHinted(t *testing.T) {
	c := DefaultClassifier()
	hints := map[string]Kind{"GHCR_USER": KindVariable, "JWT_SECRET": KindVariable, "DEPLOY_ID": KindSecret}
	for key, want := range map[string]Kind{
		"GHCR_USER":  KindVariable, // no rule: hint
		"JWT_SECRET": KindSecret,   // rule beats hint
		"DEPLOY_ID":  KindSecret,   // hint
		"UNKNOWN":    KindSecret,   // fallback
		"APP_PORT":   KindVariable, // rule
	} {
		if got := c.ClassifyHinted(key, hints); got != want {
			t.Errorf("%s: got %v want %v", key, got, want)
		}
	}
}
