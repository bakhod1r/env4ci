package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a release version: major, minor, patch.
type Version [3]int

// ParseVersion reads "0.3.1" or "v0.3.1". Anything after "-" or "+"
// (pre-release, build, git describe suffix) is ignored.
func ParseVersion(s string) (Version, error) {
	var v Version
	core := strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != len(v) {
		return v, fmt.Errorf("version %q: want major.minor.patch", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("version %q: want major.minor.patch", s)
		}
		v[i] = n
	}
	return v, nil
}

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}
