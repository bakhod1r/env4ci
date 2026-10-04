package application

// AuthKey is a setting env4ci itself reads (not a CI variable).
type AuthKey struct {
	Name    string
	Comment string
}

// AuthKeys lists the settings each target needs, in file order.
func AuthKeys(targets []string) []AuthKey {
	var out []AuthKey
	for _, t := range targets {
		switch t {
		case "github":
			out = append(out, AuthKey{"GITHUB_TOKEN", "github: fine-grained token, Secrets + Variables read/write (empty = gh auth token)"})
		case "gitlab":
			out = append(out,
				AuthKey{"GITLAB_TOKEN", "gitlab: token with api scope, Maintainer role"},
				AuthKey{"GITLAB_URL", "gitlab: self-hosted base URL (empty = gitlab.com or targets.gitlab.base_url)"})
		case "vault":
			out = append(out,
				AuthKey{"VAULT_ADDR", "vault: address (empty = targets.vault.address)"},
				AuthKey{"VAULT_TOKEN", "vault: token with read/create/update on the KV path (empty = ~/.vault-token)"},
				AuthKey{"VAULT_NAMESPACE", "vault: Enterprise/HCP namespace (optional)"})
		}
	}
	return out
}

// IsAuthKey reports whether name is an env4ci setting that may be loaded
// from the auth file.
func IsAuthKey(name string) bool {
	for _, k := range AuthKeys([]string{"github", "gitlab", "vault"}) {
		if k.Name == name {
			return true
		}
	}
	return name == "GH_TOKEN"
}
