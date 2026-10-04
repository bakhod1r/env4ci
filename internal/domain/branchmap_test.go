package domain

import "testing"

func TestBranchMapEnvironment(t *testing.T) {
	m := BranchMap{
		"main":         "production",
		"develop":      "staging",
		"release/*":    "staging",
		"release/v2.*": "preprod",
		"*":            "dev",
		"hotfix/*":     "production",
	}
	cases := map[string]string{
		"main":         "production",
		"develop":      "staging",
		"release/1.4":  "staging",
		"release/v2.1": "preprod", // longer glob wins
		"hotfix/login": "production",
		"feature/x":    "", // "*" does not cross "/"
		"random":       "dev",
	}
	for branch, want := range cases {
		got, ok := m.Environment(branch)
		if want == "" {
			if ok {
				t.Errorf("%s: got %q, want no match", branch, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%s: got %q,%v want %q", branch, got, ok, want)
		}
	}
}

func TestBranchMapEmpty(t *testing.T) {
	if _, ok := BranchMap(nil).Environment("main"); ok {
		t.Fatal("nil map matched")
	}
}
