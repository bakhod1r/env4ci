package domain

import (
	"path"
	"sort"
)

// BranchMap maps git branches (exact names or globs like "release/*") to
// CI environments.
type BranchMap map[string]string

// Environment returns the environment for branch. An exact name wins over a
// glob; among globs the longest pattern wins, then alphabetical order, so
// the result never depends on map iteration order.
func (m BranchMap) Environment(branch string) (env string, ok bool) {
	if env, ok := m[branch]; ok {
		return env, true
	}
	patterns := make([]string, 0, len(m))
	for p := range m {
		patterns = append(patterns, p)
	}
	sort.Slice(patterns, func(i, j int) bool {
		if len(patterns[i]) != len(patterns[j]) {
			return len(patterns[i]) > len(patterns[j])
		}
		return patterns[i] < patterns[j]
	})
	for _, p := range patterns {
		if matched, _ := path.Match(p, branch); matched {
			return m[p], true
		}
	}
	return "", false
}
