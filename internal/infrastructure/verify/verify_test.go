package verify

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func newKey(t *testing.T, passphrase string) (pemStr string, pub ssh.PublicKey) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	var block *pem.Block
	var err error
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(priv)
	return string(pem.EncodeToMemory(block)), signer.PublicKey()
}

// sshServer accepts only `authorized` for user "deploy".
func sshServer(t *testing.T, authorized ssh.PublicKey) (addr, knownHostsLine string) {
	t.Helper()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPriv)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if meta.User() == "deploy" && bytes.Equal(key.Marshal(), authorized.Marshal()) {
				return nil, nil
			}
			return nil, io.EOF
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(ssh.Prohibited, "no")
				}
			}()
		}
	}()
	addr = ln.Addr().String()
	return addr, knownhosts.Line([]string{knownhosts.Normalize(addr)}, hostSigner.PublicKey())
}

func TestSSH(t *testing.T) {
	good, goodPub := newKey(t, "")
	other, _ := newKey(t, "")
	locked, lockedPub := newKey(t, "s3cret")
	addr, kh := sshServer(t, goodPub)
	lockedAddr, lockedKH := sshServer(t, lockedPub)
	_, wrongKH := sshServer(t, goodPub) // a different server's host key
	host, port, _ := net.SplitHostPort(addr)

	cases := []struct {
		name    string
		login   SSHLogin
		wantErr string
	}{
		{"valid key, host:port in host", SSHLogin{Key: good, Host: addr, User: "deploy", KnownHosts: kh}, ""},
		{"valid key, separate port", SSHLogin{Key: good, Host: host, Port: port, User: "deploy", KnownHosts: kh}, ""},
		{"escaped newlines tolerated", SSHLogin{Key: strings.ReplaceAll(good, "\n", `\n`), Host: addr, User: "deploy", KnownHosts: kh}, ""},
		{"key not authorized", SSHLogin{Key: other, Host: addr, User: "deploy", KnownHosts: kh}, "rejected the key"},
		{"wrong user", SSHLogin{Key: good, Host: addr, User: "root", KnownHosts: kh}, "rejected the key"},
		{"unknown host key", SSHLogin{Key: good, Host: addr, User: "deploy", KnownHosts: "# empty"}, "not in known_hosts"},
		{"changed host key", SSHLogin{Key: good, Host: addr, User: "deploy", KnownHosts: strings.Replace(wrongKH, knownhosts.Normalize(strings.Fields(wrongKH)[0]), knownhosts.Normalize(addr), 1)}, "does NOT match"},
		{"garbage key", SSHLogin{Key: "-----BEGIN OPENSSH PRIVATE KEY-----\nnope\n-----END OPENSSH PRIVATE KEY-----"}, "not a valid"},
		{"parse-only without host", SSHLogin{Key: good}, ""},
		{"passphrase missing", SSHLogin{Key: locked}, "passphrase-protected"},
		{"passphrase wrong", SSHLogin{Key: locked, Passphrase: "nope"}, "passphrase is wrong"},
		{"passphrase ok", SSHLogin{Key: locked, Passphrase: "s3cret", Host: lockedAddr, User: "deploy", KnownHosts: lockedKH}, ""},
		{"server down", SSHLogin{Key: good, Host: "127.0.0.1:1", User: "deploy", KnownHosts: kh}, "connect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := SSH(context.Background(), tc.login)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("error leaks secret: %v", err)
			}
		})
	}
}

// fakeRegistry implements Bearer (ghcr/Docker Hub style) or Basic auth.
func fakeRegistry(t *testing.T, scheme string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		valid := ok && u == "bob" && p == "good-token"
		switch {
		case r.URL.Path == "/v2/" && scheme == "basic":
			if valid {
				return
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			w.WriteHeader(401)
		case r.URL.Path == "/v2/":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="reg.test",scope="repository:x:pull"`)
			w.WriteHeader(401)
		case r.URL.Path == "/token":
			if r.URL.Query().Get("service") != "reg.test" || r.URL.Query().Has("scope") {
				t.Errorf("bad token query %q", r.URL.RawQuery)
			}
			if !valid {
				w.WriteHeader(403)
				return
			}
			io.WriteString(w, `{"token":"abc"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRegistryLogin(t *testing.T) {
	for _, scheme := range []string{"bearer", "basic"} {
		srv := fakeRegistry(t, scheme)
		r := &Registry{Endpoint: func(string) string { return srv.URL }}
		ctx := context.Background()
		if err := r.Login(ctx, "reg.test", "bob", "good-token"); err != nil {
			t.Errorf("%s good: %v", scheme, err)
		}
		err := r.Login(ctx, "reg.test", "bob", "bad-token")
		if err == nil || !strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "bad-token") {
			t.Errorf("%s bad: %v", scheme, err)
		}
	}
}

func TestRegistryNotARegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	r := &Registry{Endpoint: func(string) string { return srv.URL }}
	if err := r.Login(context.Background(), "x", "u", "p"); err == nil || !strings.Contains(err.Error(), "not a registry") {
		t.Fatalf("err = %v", err)
	}
}

func TestEndpoint(t *testing.T) {
	r := &Registry{}
	for in, want := range map[string]string{"ghcr.io": "https://ghcr.io", "docker.io": "https://registry-1.docker.io", "https://reg.corp/": "https://reg.corp"} {
		if got := r.endpoint(in); got != want {
			t.Errorf("endpoint(%q) = %q", in, got)
		}
	}
}

func TestChecksBuildsNamesWithoutValues(t *testing.T) {
	good, _ := newKey(t, "")
	vars := []domain.Variable{
		{Key: "SSH_PRIVATE_KEY", Value: good}, {Key: "SSH_HOST", Value: "example.com"}, {Key: "SSH_USER", Value: "deploy"},
		{Key: "GHCR_TOKEN", Value: "ghp_secret"}, {Key: "REGISTRY_PASSWORD", Value: "pw"},
	}
	cs := Checks(vars, domain.DetectSSH(vars), domain.DetectRegistry(vars))
	if len(cs) != 3 {
		t.Fatalf("got %d checks", len(cs))
	}
	if cs[0].Name != "ssh deploy@example.com (SSH_PRIVATE_KEY)" || cs[1].Name != "registry ghcr.io (GHCR_TOKEN)" {
		t.Fatalf("names: %q %q", cs[0].Name, cs[1].Name)
	}
	for _, c := range cs {
		if strings.Contains(c.Name, "ghp_secret") || strings.Contains(c.Name, "PRIVATE KEY-----") {
			t.Fatalf("name leaks value: %q", c.Name)
		}
	}
	if err := cs[2].Run(context.Background()); err == nil || !strings.Contains(err.Error(), "registry host unknown") {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistryInsecureAndPublic(t *testing.T) {
	srv := fakeRegistry(t, "basic") // httptest = plain HTTP
	host := strings.TrimPrefix(srv.URL, "http://")
	r := &Registry{Insecure: true}
	if got := r.endpoint(host); got != srv.URL {
		t.Fatalf("endpoint = %q", got)
	}
	if err := r.Login(context.Background(), host, "bob", "good-token"); err != nil {
		t.Fatalf("insecure login: %v", err)
	}
	vars := []domain.Variable{{Key: "U", Value: "bob"}, {Key: "P", Value: "good-token"}}
	cs := Checks(vars, nil, []domain.RegistryCredential{
		{Registry: host, Username: "U", Password: "P", Insecure: true},
		{Registry: "ghcr.io", Public: true},
	})
	if err := cs[0].Run(context.Background()); err != nil {
		t.Fatalf("insecure check: %v", err)
	}
	if !strings.Contains(cs[1].Name, "public") || cs[1].Run(context.Background()) != nil {
		t.Fatalf("public check: %q", cs[1].Name)
	}
}

func TestRegistryCustomCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); ok && u == "bob" && p == "good-token" {
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="r"`)
		w.WriteHeader(401)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	vars := []domain.Variable{{Key: "U", Value: "bob"}, {Key: "P", Value: "good-token"}}

	without := Checks(vars, nil, []domain.RegistryCredential{{Registry: host, Username: "U", Password: "P"}})
	if err := without[0].Run(context.Background()); err == nil {
		t.Fatal("self-signed registry must fail without ca_file")
	}
	with := Checks(vars, nil, []domain.RegistryCredential{{Registry: host, Username: "U", Password: "P", CAFile: caFile}})
	if err := with[0].Run(context.Background()); err != nil {
		t.Fatalf("with ca_file: %v", err)
	}
	for _, bad := range []string{filepath.Join(t.TempDir(), "missing.pem"), caFileWith(t, "not a cert")} {
		cs := Checks(vars, nil, []domain.RegistryCredential{{Registry: host, Username: "U", Password: "P", CAFile: bad}})
		if err := cs[0].Run(context.Background()); err == nil || !strings.Contains(err.Error(), "ca_file") {
			t.Fatalf("%s: err = %v", bad, err)
		}
	}
}

func caFileWith(t *testing.T, content string) string {
	p := filepath.Join(t.TempDir(), "bad.pem")
	os.WriteFile(p, []byte(content), 0o600)
	return p
}

func certPEM(srv *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}
