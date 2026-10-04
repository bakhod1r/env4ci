package verify

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func TestSSHCheckRunParseOnly(t *testing.T) {
	good, _ := newKey(t, "")
	vars := []domain.Variable{{Key: "DEPLOY_KEY", Value: good}}
	cs := Checks(vars, domain.DetectSSH(vars), nil)
	if len(cs) != 1 || cs[0].Name != "ssh DEPLOY_KEY" || cs[0].Run(context.Background()) != nil {
		t.Fatalf("%+v", cs)
	}
}

func TestHostKeyCallbackHomeKnownHosts(t *testing.T) {
	good, pub := newKey(t, "")
	addr, kh := sshServer(t, pub)
	home := t.TempDir()
	old := userHomeDir
	defer func() { userHomeDir = old }()
	userHomeDir = func() (string, error) { return home, nil }

	// No ~/.ssh/known_hosts and no SSH_KNOWN_HOSTS: refuse.
	if err := SSH(context.Background(), SSHLogin{Key: good, Host: addr, User: "deploy"}); err == nil || !strings.Contains(err.Error(), "no known_hosts") {
		t.Fatalf("err = %v", err)
	}
	// With ~/.ssh/known_hosts: works.
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(kh+"\n"), 0o600)
	if err := SSH(context.Background(), SSHLogin{Key: good, Host: addr, User: "deploy"}); err != nil {
		t.Fatalf("home known_hosts: %v", err)
	}
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	if _, err := hostKeyCallback(""); err == nil {
		t.Fatal("want home error")
	}
}

func TestHostKeyCallbackTempFailures(t *testing.T) {
	old := createTemp
	defer func() { createTemp = old }()
	createTemp = func(string, string) (*os.File, error) { return nil, errors.New("no tmp") }
	if _, err := hostKeyCallback("x"); err == nil {
		t.Fatal("create: want error")
	}
	createTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(t.TempDir(), pattern)
		if err == nil {
			f.Close() // writes will fail
		}
		return f, err
	}
	if _, err := hostKeyCallback("x"); err == nil {
		t.Fatal("write: want error")
	}
}

func TestSSHHandshakeFailure(t *testing.T) {
	good, _ := newKey(t, "")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("not ssh\r\n"))
			c.Close()
		}
	}()
	err := SSH(context.Background(), SSHLogin{Key: good, Host: ln.Addr().String(), User: "u", KnownHosts: "# none"})
	if err == nil || !strings.HasPrefix(err.Error(), "ssh ") {
		t.Fatalf("err = %v", err)
	}
}

func registryWith(t *testing.T, h http.HandlerFunc) *Registry {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Registry{Endpoint: func(string) string { return srv.URL }}
}

func TestRegistryErrorPaths(t *testing.T) {
	ctx := context.Background()
	challenge := func(v string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v2/" && r.Header.Get("Authorization") == "" {
				w.Header().Set("WWW-Authenticate", v)
				w.WriteHeader(401)
				return
			}
			if r.URL.Path == "/v2/" { // basic login attempt
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, `{}`) // token endpoint without token
		}
	}
	cases := map[string]struct {
		h    http.HandlerFunc
		want string
	}{
		"bad realm":       {challenge(`Bearer realm="",service="x"`), "bad auth challenge"},
		"no token":        {nil, "returned no token"},
		"unknown scheme":  {challenge(`Digest realm="x"`), "unsupported auth scheme"},
		"realm down":      {challenge(`Bearer realm="http://127.0.0.1:1/token"`), "127.0.0.1:1"},
		"basic login 500": {challenge(`Basic realm="r"`), "login failed (HTTP 500)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var r *Registry
			if tc.h == nil {
				var srv *httptest.Server
				srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Path == "/v2/" {
						w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token"`)
						w.WriteHeader(401)
						return
					}
					io.WriteString(w, `{}`)
				}))
				t.Cleanup(srv.Close)
				r = &Registry{Endpoint: func(string) string { return srv.URL }}
			} else {
				r = registryWith(t, tc.h)
			}
			r.HTTP = &http.Client{}
			if err := r.Login(ctx, "reg", "u", "p"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}

	bad := &Registry{Endpoint: func(string) string { return "http://bad\x7fhost" }}
	if err := bad.Login(ctx, "x", "u", "p"); err == nil {
		t.Fatal("bad url: want error")
	}
}

func TestSystemCertPoolFallbackAndFirst(t *testing.T) {
	old := systemCertPool
	defer func() { systemCertPool = old }()
	systemCertPool = func() (*x509.CertPool, error) { return nil, errors.New("unsupported") }
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, certPEM(srv), 0o600)
	if _, err := clientWithCA(ca); err != nil {
		t.Fatal(err)
	}
	if first("", "") != "" {
		t.Fatal("first")
	}
}
