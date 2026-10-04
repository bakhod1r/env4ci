package domain

import (
	"sort"
	"strings"
)

// SSHCredential names the variables that make up one SSH login. Fields are
// variable names, never values. Empty Host or User means the key can only be
// parsed, not tried against a server.
type SSHCredential struct {
	Key, Host, User, Port, KnownHosts, Passphrase string
}

// RegistryCredential is one container registry login. Registry is a host
// (ghcr.io, docker.io, ...); Username and Password are variable names.
type RegistryCredential struct {
	Registry, Username, Password string
	// Public registries serve images without login; nothing is checked.
	Public bool
	// Insecure: self-hosted registry over plain HTTP.
	Insecure bool
	// CAFile: PEM bundle for a self-hosted registry with its own CA.
	CAFile string
}

// LooksLikePrivateKey reports whether a value is a PEM/OpenSSH private key.
func LooksLikePrivateKey(value string) bool {
	return strings.Contains(value, "-----BEGIN") && strings.Contains(value, "PRIVATE KEY-----")
}

// DetectSSH finds private keys and pairs each with host/user/port variables by
// naming convention: DEPLOY_SSH_KEY pairs with DEPLOY_HOST, DEPLOY_USER ...,
// falling back to SSH_HOST, SSH_USER ...
func DetectSSH(vars []Variable) []SSHCredential {
	have := keySet(vars)
	var out []SSHCredential
	for _, v := range vars {
		if !LooksLikePrivateKey(v.Value) {
			continue
		}
		p := keyPrefix(v.Key, "_SSH_PRIVATE_KEY", "_PRIVATE_KEY", "_SSH_KEY", "_KEY")
		pick := func(suffixes ...string) string {
			var names []string
			for _, s := range suffixes {
				if p != "" {
					names = append(names, p+"_"+s, p+"_SSH_"+s)
				}
			}
			for _, s := range suffixes {
				names = append(names, "SSH_"+s)
			}
			for _, n := range names {
				if have[n] {
					return n
				}
			}
			return ""
		}
		out = append(out, SSHCredential{
			Key:        v.Key,
			Host:       pick("HOST", "SERVER"),
			User:       pick("USER", "USERNAME"),
			Port:       pick("PORT"),
			KnownHosts: pick("KNOWN_HOSTS"),
			Passphrase: pick("PASSPHRASE", "KEY_PASSPHRASE"),
		})
	}
	return out
}

// DetectRegistry finds registry logins by naming convention:
// GHCR_* -> ghcr.io, DOCKERHUB_* / DOCKER_* -> docker.io, and
// REGISTRY_USER/REGISTRY_PASSWORD with the host in REGISTRY / REGISTRY_HOST.
func DetectRegistry(vars []Variable) []RegistryCredential {
	have := keySet(vars)
	values := map[string]string{}
	for _, v := range vars {
		values[v.Key] = v.Value
	}
	pick := func(names ...string) string {
		for _, n := range names {
			if have[n] {
				return n
			}
		}
		return ""
	}
	var out []RegistryCredential
	if pw := pick("GHCR_TOKEN", "GHCR_PAT", "GHCR_PASSWORD"); pw != "" {
		out = append(out, RegistryCredential{Registry: "ghcr.io", Username: pick("GHCR_USERNAME", "GHCR_USER"), Password: pw})
	}
	if pw := pick("DOCKERHUB_TOKEN", "DOCKERHUB_PASSWORD", "DOCKER_TOKEN", "DOCKER_PASSWORD"); pw != "" {
		out = append(out, RegistryCredential{Registry: "docker.io", Username: pick("DOCKERHUB_USERNAME", "DOCKERHUB_USER", "DOCKER_USERNAME", "DOCKER_USER"), Password: pw})
	}
	if pw := pick("REGISTRY_PASSWORD", "REGISTRY_TOKEN"); pw != "" {
		host := ""
		if h := pick("REGISTRY", "REGISTRY_HOST", "REGISTRY_URL"); h != "" {
			host = values[h]
		}
		out = append(out, RegistryCredential{Registry: host, Username: pick("REGISTRY_USER", "REGISTRY_USERNAME"), Password: pw})
	}
	// Self-hosted / extra registries: <X>_REGISTRY holds the host, with
	// <X>_REGISTRY_USER(NAME) and <X>_REGISTRY_PASSWORD|TOKEN beside it
	// (HARBOR_REGISTRY, GHE_REGISTRY, DOCKER_REGISTRY ...).
	var prefixes []string
	for _, v := range vars {
		if p, ok := strings.CutSuffix(v.Key, "_REGISTRY"); ok && p != "" && v.Value != "" {
			prefixes = append(prefixes, p)
		}
	}
	sort.Strings(prefixes)
	for _, p := range prefixes {
		pw := pick(p+"_REGISTRY_PASSWORD", p+"_REGISTRY_TOKEN", p+"_PASSWORD", p+"_TOKEN")
		if pw == "" {
			continue
		}
		out = append(out, RegistryCredential{Registry: values[p+"_REGISTRY"],
			Username: pick(p+"_REGISTRY_USER", p+"_REGISTRY_USERNAME", p+"_USERNAME", p+"_USER"), Password: pw})
	}
	return dedupeRegistries(out)
}

// dedupeRegistries keeps the first credential per password variable
// (DOCKER_REGISTRY + DOCKER_PASSWORD would otherwise appear twice).
func dedupeRegistries(in []RegistryCredential) []RegistryCredential {
	seen := map[string]int{}
	var out []RegistryCredential
	for _, c := range in {
		if i, ok := seen[c.Password]; ok {
			if out[i].Registry == "docker.io" && c.Registry != "" {
				out[i].Registry = c.Registry // DOCKER_REGISTRY names the real host
			}
			continue
		}
		seen[c.Password] = len(out)
		out = append(out, c)
	}
	return out
}

// Keys lists every variable name a credential depends on.
func (c SSHCredential) Keys() []string {
	return nonEmpty(c.Key, c.Host, c.User, c.Port, c.KnownHosts, c.Passphrase)
}

func (c RegistryCredential) Keys() []string { return nonEmpty(c.Username, c.Password) }

// keyPrefix strips the first matching suffix; "SSH_PRIVATE_KEY" -> "".
func keyPrefix(key string, suffixes ...string) string {
	if key == "SSH_PRIVATE_KEY" || key == "SSH_KEY" || key == "PRIVATE_KEY" {
		return ""
	}
	for _, s := range suffixes {
		if strings.HasSuffix(key, s) {
			return strings.TrimSuffix(key, s)
		}
	}
	return key
}

func keySet(vars []Variable) map[string]bool {
	m := make(map[string]bool, len(vars))
	for _, v := range vars {
		m[v.Key] = true
	}
	return m
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// Family groups credential keys that belong together in a file:
// "ssh" (SSH_*, *_SSH_*, *_PRIVATE_KEY, *_KNOWN_HOSTS), "registry"
// (REGISTRY*, *_REGISTRY*, GHCR_*, DOCKERHUB_*, DOCKER_*), or "" for others.
func Family(key string) string {
	k := strings.ToUpper(key)
	switch {
	case strings.HasPrefix(k, "SSH_") || strings.Contains(k, "_SSH_") || strings.HasSuffix(k, "_SSH_KEY") ||
		strings.HasSuffix(k, "_PRIVATE_KEY") || strings.HasSuffix(k, "KNOWN_HOSTS"):
		return "ssh"
	case strings.HasPrefix(k, "REGISTRY") || strings.Contains(k, "_REGISTRY") || strings.HasPrefix(k, "GHCR_") ||
		strings.HasPrefix(k, "DOCKERHUB_") || strings.HasPrefix(k, "DOCKER_"):
		return "registry"
	}
	return ""
}

// KnownHostsFor names the known-hosts variable env4ci verify reads for an
// SSH key variable: SSH_PRIVATE_KEY -> SSH_KNOWN_HOSTS, DEPLOY_SSH_KEY ->
// DEPLOY_KNOWN_HOSTS. Empty when key is not an SSH key name.
func KnownHostsFor(key string) string {
	k := strings.ToUpper(key)
	if k != "SSH_PRIVATE_KEY" && k != "SSH_KEY" && !strings.HasSuffix(k, "_SSH_KEY") &&
		!strings.HasSuffix(k, "_SSH_PRIVATE_KEY") && !strings.HasSuffix(k, "_PRIVATE_KEY") {
		return ""
	}
	if p := keyPrefix(k, "_SSH_PRIVATE_KEY", "_PRIVATE_KEY", "_SSH_KEY"); p != "" {
		return p + "_KNOWN_HOSTS"
	}
	return "SSH_KNOWN_HOSTS"
}
