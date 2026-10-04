package domain

import "sort"

// Action is what applying a plan would do to one key.
type Action int

const (
	ActionNoop         Action = iota // identical locally and remotely
	ActionCreate                     // local only
	ActionUpdate                     // value or kind differs
	ActionUnverifiable               // remote value hidden; will be re-written
	ActionRemoteOnly                 // remote only; left alone unless pruning
)

func (a Action) Symbol() string {
	return [...]string{" ", "+", "~", "?", "-"}[a]
}

func (a Action) String() string {
	return [...]string{"unchanged", "new", "changed", "unverifiable", "remote only"}[a]
}

// Change is one line of a plan. It never carries values, so a plan is always
// safe to print.
type Change struct {
	Key    string
	Kind   Kind
	Action Action
}

type Plan struct {
	Changes []Change
}

// ComputePlan compares local variables against remote state.
func ComputePlan(local []Variable, remote []Remote) Plan {
	byKey := make(map[string]Remote, len(remote))
	for _, r := range remote {
		byKey[r.Key] = r
	}

	var changes []Change
	seen := make(map[string]bool, len(local))
	for _, v := range local {
		seen[v.Key] = true
		r, ok := byKey[v.Key]
		action := ActionNoop
		switch {
		case !ok:
			action = ActionCreate
		case r.Kind != v.Kind:
			action = ActionUpdate
		case !r.Known:
			action = ActionUnverifiable
		case r.Value != v.Value:
			action = ActionUpdate
		}
		changes = append(changes, Change{Key: v.Key, Kind: v.Kind, Action: action})
	}
	for _, r := range remote {
		if !seen[r.Key] {
			changes = append(changes, Change{Key: r.Key, Kind: r.Kind, Action: ActionRemoteOnly})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Key < changes[j].Key })
	return Plan{Changes: changes}
}

// HasWrites reports whether applying the plan would write anything.
func (p Plan) HasWrites() bool {
	for _, c := range p.Changes {
		if c.Action == ActionCreate || c.Action == ActionUpdate || c.Action == ActionUnverifiable {
			return true
		}
	}
	return false
}
