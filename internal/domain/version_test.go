package domain

import "testing"

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]Version{
		"0.3.1":              {0, 3, 1},
		"v0.3.1":             {0, 3, 1},
		"v1.20.0":            {1, 20, 0},
		"v0.3.1-2-g1160a7f":  {0, 3, 1}, // git describe: ahead of the tag
		"1.2.3+build.5":      {1, 2, 3},
		"v10.0.0-rc.1+build": {10, 0, 0},
	} {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "dev", "v1.2", "1.2.3.4", "1.x.3", "1.-2.3", "v", "1..3"} {
		if v, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want error", in, v)
		}
	}
}

func TestVersionLessAndString(t *testing.T) {
	for _, tc := range []struct {
		a, b Version
		less bool
	}{
		{Version{0, 3, 0}, Version{0, 3, 1}, true},
		{Version{0, 3, 1}, Version{0, 3, 1}, false},
		{Version{0, 9, 9}, Version{1, 0, 0}, true},
		{Version{0, 10, 0}, Version{0, 9, 9}, false},
		{Version{1, 0, 0}, Version{0, 9, 9}, false},
		{Version{0, 2, 9}, Version{0, 3, 0}, true},
	} {
		if got := tc.a.Less(tc.b); got != tc.less {
			t.Errorf("%v.Less(%v) = %v", tc.a, tc.b, got)
		}
	}
	if s := (Version{0, 3, 1}).String(); s != "0.3.1" {
		t.Errorf("String = %q", s)
	}
}
