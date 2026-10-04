package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

// fakeKV is a minimal KV v2 secret with versions and check-and-set.
type fakeKV struct {
	mu      sync.Mutex
	data    map[string]any
	version int
	writes  int
}

func (f *fakeKV) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("X-Vault-Token") != "tok" {
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string][]string{"errors": {"permission denied"}})
			return
		}
		if r.URL.Path != "/v1/kv/data/app/production" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-Vault-Namespace") != "team" {
			t.Errorf("namespace = %q", r.Header.Get("X-Vault-Namespace"))
		}
		switch r.Method {
		case http.MethodGet:
			if f.version == 0 {
				w.WriteHeader(404)
				w.Write([]byte(`{"errors":[]}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": f.data, "metadata": map[string]int{"version": f.version}}})
		case http.MethodPost:
			var body struct {
				Data    map[string]any `json:"data"`
				Options struct {
					CAS int `json:"cas"`
				} `json:"options"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Options.CAS != f.version {
				w.WriteHeader(400)
				w.Write([]byte(`{"errors":["check-and-set parameter did not match the current version"]}`))
				return
			}
			f.data, f.version = body.Data, f.version+1
			f.writes++
			w.Write([]byte(`{"data":{}}`))
		}
	})
}

func newClient(t *testing.T, kv *fakeKV) *Client {
	srv := httptest.NewServer(kv.handler(t))
	t.Cleanup(srv.Close)
	return &Client{Address: srv.URL + "/", Token: "tok", Namespace: "team", Mount: "kv/", Path: "/app/production"}
}

func TestCreateThenMergeAndDelete(t *testing.T) {
	kv := &fakeKV{}
	c := newClient(t, kv)
	ctx := context.Background()

	if rs, err := c.List(ctx); err != nil || len(rs) != 0 {
		t.Fatalf("empty list: %v %v", rs, err)
	}
	if err := c.ApplyBatch(ctx, []domain.Variable{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyBatch(ctx, []domain.Variable{{Key: "B", Value: "3"}}, []string{"A"}); err != nil {
		t.Fatal(err)
	}
	rs, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Key != "B" || rs[0].Value != "3" || !rs[0].Known || kv.writes != 2 || kv.version != 2 {
		t.Fatalf("rs=%+v writes=%d version=%d", rs, kv.writes, kv.version)
	}
	if c.Name() != "vault:kv/app/production" {
		t.Fatalf("name = %q", c.Name())
	}
}

func TestNonStringValues(t *testing.T) {
	kv := &fakeKV{data: map[string]any{"PORT": 8080.0, "ON": true, "S": "x"}, version: 3}
	rs, err := newClient(t, kv).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rs {
		got[r.Key] = r.Value
	}
	if got["PORT"] != "8080" || got["ON"] != "true" || got["S"] != "x" {
		t.Fatalf("got %v", got)
	}
}

func TestConcurrentChangeDetected(t *testing.T) {
	kv := &fakeKV{data: map[string]any{"A": "1"}, version: 1}
	c := newClient(t, kv)
	// Another writer bumps the version between our read and write.
	c.HTTP = doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			kv.mu.Lock()
			kv.version++
			kv.mu.Unlock()
		}
		return http.DefaultClient.Do(r)
	})
	err := c.ApplyBatch(context.Background(), []domain.Variable{{Key: "A", Value: "2"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "changed during push") {
		t.Fatalf("err = %v", err)
	}
}

func TestPermissionDenied(t *testing.T) {
	kv := &fakeKV{}
	c := newClient(t, kv)
	c.Token = "bad"
	_, err := c.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v", err)
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
