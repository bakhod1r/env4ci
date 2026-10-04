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
	Sources     []string
}

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
			c.Stages, c.Sources = nil, nil
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
