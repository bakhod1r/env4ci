package domain

import "time"

// AuditEntry records one push: who wrote which keys where. It never holds
// values, so the log is safe to commit.
type AuditEntry struct {
	Time        time.Time     `json:"time"`
	Actor       string        `json:"actor"`
	Provider    string        `json:"provider"`
	Target      string        `json:"target"`
	Environment string        `json:"environment,omitempty"`
	Changes     []AuditChange `json:"changes"`
	Result      string        `json:"result"` // "ok" or the error text
}

// AuditChange is one key a push wrote or deleted.
type AuditChange struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"`
	Action string `json:"action"` // create | update | delete
}

// AuditChanges lists what applying p does: writes, plus deletes when prune.
func AuditChanges(p Plan, prune bool) []AuditChange {
	var out []AuditChange
	for _, c := range p.Changes {
		var a string
		switch c.Action {
		case ActionCreate:
			a = "create"
		case ActionUpdate, ActionUnverifiable:
			a = "update"
		case ActionRemoteOnly:
			if !prune {
				continue
			}
			a = "delete"
		default:
			continue
		}
		out = append(out, AuditChange{Key: c.Key, Kind: c.Kind.String(), Action: a})
	}
	return out
}
