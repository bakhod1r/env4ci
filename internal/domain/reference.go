package domain

import "sort"

// Reference is a variable a CI pipeline expects to exist in CI settings.
// Environment is empty for repository / "*" scope. Stages lists where it is
// used: the GitLab stage, or the GitHub job id (GitHub has no stages).
type Reference struct {
	Key         string
	Kind        Kind
	Provider    string
	Environment string
	Stages      []string
	Branches    []string // BranchAll, branch names/patterns, or BranchTags
	Sources     []string
}

const (
	BranchAll  = "*"      // runs on every branch
	BranchTags = "(tags)" // runs on tags only
)

// MergeReferences dedupes by provider+environment+key, unions stages and
// sources, and promotes kind to secret if any occurrence is a secret.
func MergeReferences(refs []Reference) []Reference {
	type id struct{ p, e, k string }
	return mergeBy(refs, func(r Reference) any { return id{r.Provider, r.Environment, r.Key} })
}

// EnvGroup is every key one environment needs, across providers.
type EnvGroup struct {
	Environment string
	Refs        []Reference
}

// GroupByEnvironment merges the same key from different providers and groups
// by environment, shared ("") first.
func GroupByEnvironment(refs []Reference) []EnvGroup {
	type id struct{ e, k string }
	merged := mergeBy(refs, func(r Reference) any { return id{r.Environment, r.Key} })
	var groups []EnvGroup
	for _, r := range merged {
		if n := len(groups); n == 0 || groups[n-1].Environment != r.Environment {
			groups = append(groups, EnvGroup{Environment: r.Environment})
		}
		g := &groups[len(groups)-1]
		g.Refs = append(g.Refs, r)
	}
	return groups
}

func mergeBy(refs []Reference, key func(Reference) any) []Reference {
	byID := map[any]*Reference{}
	var order []any
	for _, r := range refs {
		i := key(r)
		cur, ok := byID[i]
		if !ok {
			c := r
			c.Stages, c.Branches, c.Sources = nil, nil, nil
			cur = &c
			byID[i] = cur
			order = append(order, i)
		}
		if r.Kind == KindSecret {
			cur.Kind = KindSecret
		}
		if cur.Provider != r.Provider {
			cur.Provider = ""
		}
		cur.Stages = union(cur.Stages, r.Stages)
		cur.Branches = normalizeBranches(union(cur.Branches, r.Branches))
		cur.Sources = union(cur.Sources, r.Sources)
	}
	out := make([]Reference, 0, len(order))
	for _, i := range order {
		out = append(out, *byID[i])
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, y := out[a], out[b]
		if x.Environment != y.Environment {
			return x.Environment < y.Environment
		}
		return x.Key < y.Key
	})
	return out
}

// GroupByBranch puts each key in every branch group it runs on. The
// BranchAll group comes first; a key that runs everywhere is only there.
func GroupByBranch(refs []Reference) []EnvGroup {
	type id struct{ b, k string }
	byBranch := map[string][]Reference{}
	for _, r := range refs {
		branches := r.Branches
		if len(branches) == 0 {
			branches = []string{BranchAll}
		}
		for _, b := range branches {
			c := r
			c.Environment = b
			byBranch[b] = append(byBranch[b], c)
		}
	}
	var names []string
	for b := range byBranch {
		names = append(names, b)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == BranchAll) != (names[j] == BranchAll) {
			return names[i] == BranchAll
		}
		return names[i] < names[j]
	})
	var out []EnvGroup
	for _, b := range names {
		merged := mergeBy(byBranch[b], func(r Reference) any { return id{b, r.Key} })
		out = append(out, EnvGroup{Environment: b, Refs: merged})
	}
	return out
}

// normalizeBranches collapses to BranchAll when any occurrence runs everywhere.
func normalizeBranches(bs []string) []string {
	for _, b := range bs {
		if b == BranchAll {
			return []string{BranchAll}
		}
	}
	sort.Strings(bs)
	return bs
}

// MissingFrom returns references whose key is absent from local.
func MissingFrom(refs []Reference, local []Variable) []Reference {
	have := make(map[string]bool, len(local))
	for _, v := range local {
		have[v.Key] = true
	}
	var out []Reference
	for _, r := range refs {
		if !have[r.Key] {
			out = append(out, r)
		}
	}
	return out
}

// MissingRemote returns references a target needs but the provider lacks.
// A target for environment env needs that environment's keys plus shared
// ("") ones; remote should include both levels.
func MissingRemote(refs []Reference, env string, remote []Remote) []Reference {
	have := make(map[string]bool, len(remote))
	for _, r := range remote {
		have[r.Key] = true
	}
	var out []Reference
	for _, r := range refs {
		if (r.Environment == "" || r.Environment == env) && !have[r.Key] {
			out = append(out, r)
		}
	}
	return out
}

func union(dst, src []string) []string {
	for _, s := range src {
		found := false
		for _, d := range dst {
			if d == s {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, s)
		}
	}
	return dst
}
