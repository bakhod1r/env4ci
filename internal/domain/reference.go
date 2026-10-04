package domain

import "sort"

// Reference is a variable a CI pipeline expects to exist in CI settings.
// Environment is empty for repository / "*" scope.
type Reference struct {
	Key         string
	Kind        Kind
	Provider    string
	Environment string
	Sources     []string
}

// MergeReferences dedupes by provider+environment+key, unions sources,
// and promotes kind to secret if any occurrence is a secret.
func MergeReferences(refs []Reference) []Reference {
	type id struct{ p, e, k string }
	byID := map[id]*Reference{}
	var order []id
	for _, r := range refs {
		i := id{r.Provider, r.Environment, r.Key}
		cur, ok := byID[i]
		if !ok {
			c := r
			c.Sources = nil
			byID[i] = &c
			order = append(order, i)
			cur = &c
		}
		if r.Kind == KindSecret {
			cur.Kind = KindSecret
		}
		for _, s := range r.Sources {
			if !contains(cur.Sources, s) {
				cur.Sources = append(cur.Sources, s)
			}
		}
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

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
