// Package verify proves credentials work before they are stored in CI:
// it logs in over SSH (host key checked, no command run) and logs in to
// container registries. Errors never contain credential values.
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/bakhod1r/env4ci/internal/application"
	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/httpx"
)

// Timeout bounds each network check.
const Timeout = 15 * time.Second

// Checks builds one application.Check per credential.
func Checks(vars []domain.Variable, sshCreds []domain.SSHCredential, regCreds []domain.RegistryCredential) []application.Check {
	val := map[string]string{}
	for _, v := range vars {
		val[v.Key] = v.Value
	}
	var out []application.Check
	for _, c := range sshCreds {
		c := c
		name := "ssh " + c.Key
		if c.Host != "" && c.User != "" {
			name = fmt.Sprintf("ssh %s@%s (%s)", val[c.User], val[c.Host], c.Key)
		}
		out = append(out, application.Check{Name: name, Keys: c.Keys(), Run: func(ctx context.Context) error {
			return SSH(ctx, SSHLogin{
				Key: val[c.Key], Passphrase: val[c.Passphrase],
				Host: val[c.Host], User: val[c.User], Port: val[c.Port],
				KnownHosts: val[c.KnownHosts],
			})
		}})
	}
	reg := &Registry{}
	for _, c := range regCreds {
		c := c
		out = append(out, application.Check{
			Name: fmt.Sprintf("registry %s (%s)", first(c.Registry, "?"), c.Password),
			Keys: c.Keys(),
			Run: func(ctx context.Context) error {
				if c.Registry == "" {
					return fmt.Errorf("registry host unknown: set REGISTRY or add checks.registry in env4ci.yaml")
				}
				return reg.Login(ctx, c.Registry, first(val[c.Username], "token"), val[c.Password])
			},
		})
	}
	return out
}

// --- SSH ---

type SSHLogin struct {
	Key, Passphrase  string
	Host, User, Port string
	KnownHosts       string // known_hosts content; empty = ~/.ssh/known_hosts
}

// SSH parses the key and, when host and user are known, completes an SSH
// handshake and public-key authentication, then disconnects. Unknown or
// changed host keys fail: a key is never sent to an unverified server.
func SSH(ctx context.Context, l SSHLogin) error {
	signer, err := parseKey(l.Key, l.Passphrase)
	if err != nil {
		return err
	}
	if l.Host == "" || l.User == "" {
		return nil // parse-only; caller reports it as such via the check name
	}
	hostKeys, err := hostKeyCallback(l.KnownHosts)
	if err != nil {
		return err
	}
	addr := l.Host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, first(l.Port, "22"))
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("connect %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	cfg := &ssh.ClientConfig{
		User:            l.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeys,
		Timeout:         Timeout,
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		var keyErr *knownhosts.KeyError
		switch {
		case errors.As(err, &keyErr) && len(keyErr.Want) == 0:
			return fmt.Errorf("host key for %s is not in known_hosts (add it, or set SSH_KNOWN_HOSTS)", addr)
		case errors.As(err, &keyErr):
			return fmt.Errorf("host key for %s does NOT match known_hosts: possible man-in-the-middle", addr)
		case strings.Contains(err.Error(), "unable to authenticate"):
			return fmt.Errorf("server %s rejected the key for user %q", addr, l.User)
		}
		return fmt.Errorf("ssh %s: %w", addr, err)
	}
	_ = ssh.NewClient(c, chans, reqs).Close()
	return nil
}

func parseKey(key, passphrase string) (ssh.Signer, error) {
	pem := []byte(strings.ReplaceAll(key, `\n`, "\n")) // tolerate single-line escaped keys
	s, err := ssh.ParsePrivateKey(pem)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if passphrase == "" {
			return nil, errors.New("private key is passphrase-protected; add a *_PASSPHRASE variable")
		}
		s, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
		if err != nil {
			return nil, errors.New("private key passphrase is wrong")
		}
	}
	if err != nil {
		return nil, errors.New("private key is not a valid PEM/OpenSSH key")
	}
	return s, nil
}

func hostKeyCallback(content string) (ssh.HostKeyCallback, error) {
	if content != "" {
		f, err := os.CreateTemp("", "env4ci-known-hosts-*")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.Remove(f.Name()) }()
		if _, err := f.WriteString(content + "\n"); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		return knownhosts.New(f.Name())
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".ssh", "known_hosts")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no known_hosts (%s) and no SSH_KNOWN_HOSTS variable: cannot verify the server", path)
	}
	return knownhosts.New(path)
}

// --- Registry ---

// Registry logs in to a Docker Registry v2 API (ghcr.io, Docker Hub, GitLab, Harbor ...).
type Registry struct {
	HTTP httpDoer
	// Endpoint maps a registry host to its API base; nil = https://host
	// (docker.io -> https://registry-1.docker.io).
	Endpoint func(host string) string
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

var challengeParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// Login performs the registry's auth flow with username/password and
// returns nil only if the registry accepted them.
func (r *Registry) Login(ctx context.Context, host, user, password string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	base := r.endpoint(host)
	resp, err := r.get(ctx, base+"/v2/", "", "")
	if err != nil {
		return fmt.Errorf("%s: %w", host, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("%s: unexpected HTTP %d from /v2/ (not a registry?)", host, resp.StatusCode)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	scheme, _, _ := strings.Cut(challenge, " ")

	switch strings.ToLower(scheme) {
	case "basic":
		resp, err = r.get(ctx, base+"/v2/", user, password)
	case "bearer":
		params := map[string]string{}
		for _, m := range challengeParam.FindAllStringSubmatch(challenge, -1) {
			params[m[1]] = m[2]
		}
		realm, err2 := url.Parse(params["realm"])
		if err2 != nil || realm.Scheme == "" {
			return fmt.Errorf("%s: bad auth challenge", host)
		}
		q := realm.Query()
		if s := params["service"]; s != "" {
			q.Set("service", s)
		}
		realm.RawQuery = q.Encode()
		resp, err = r.get(ctx, realm.String(), user, password)
		if err == nil && resp.StatusCode == http.StatusOK {
			var tok struct {
				Token       string `json:"token"`
				AccessToken string `json:"access_token"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok) != nil || tok.Token+tok.AccessToken == "" {
				resp.Body.Close()
				return fmt.Errorf("%s: login returned no token", host)
			}
		}
	default:
		return fmt.Errorf("%s: unsupported auth scheme %q", host, scheme)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", host, err)
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s rejected the username/token (HTTP %d)", host, resp.StatusCode)
	}
	return fmt.Errorf("%s: login failed (HTTP %d)", host, resp.StatusCode)
}

func (r *Registry) endpoint(host string) string {
	if r.Endpoint != nil {
		return r.Endpoint(host)
	}
	host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://"), "/")
	if host == "docker.io" || host == "index.docker.io" {
		host = "registry-1.docker.io"
	}
	return "https://" + host
}

func (r *Registry) get(ctx context.Context, u, user, password string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	if r.HTTP == nil {
		r.HTTP = httpx.New()
	}
	return r.HTTP.Do(req)
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
