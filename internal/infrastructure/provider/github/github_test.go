package github

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/nacl/box"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func TestSealDecryptable(t *testing.T) {
	pub, priv, _ := box.GenerateKey(rand.Reader)
	sealed, err := Seal(base64.StdEncoding.EncodeToString(pub[:]), "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(sealed)
	plain, ok := box.OpenAnonymous(nil, raw, pub, priv)
	if !ok || string(plain) != "s3cret" {
		t.Fatalf("decrypt failed: %q", plain)
	}
}

func TestClientFlow(t *testing.T) {
	pub, priv, _ := box.GenerateKey(rand.Reader)
	var calls []string
	var secretBody map[string]string

	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/environments/prod/secrets", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total_count":1,"secrets":[{"name":"S"}]}`)
	})
	mux.HandleFunc("GET /repos/o/r/environments/prod/variables", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total_count":1,"variables":[{"name":"V","value":"1"}]}`)
	})
	mux.HandleFunc("GET /repos/o/r/environments/prod/secrets/public-key", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"key_id": "k1", "key": base64.StdEncoding.EncodeToString(pub[:])})
	})
	mux.HandleFunc("PUT /repos/o/r/environments/prod/secrets/S", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&secretBody)
		w.WriteHeader(204)
	})
	mux.HandleFunc("PATCH /repos/o/r/environments/prod/variables/NEW", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "patch")
		w.WriteHeader(404)
	})
	mux.HandleFunc("POST /repos/o/r/environments/prod/variables", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Error("missing auth header")
		}
		calls = append(calls, "post")
		w.WriteHeader(201)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "tok", Repo: "o/r", Environment: "prod"}
	ctx := context.Background()

	remote, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remote) != 2 || remote[0].Known || !remote[1].Known || remote[1].Value != "1" {
		t.Fatalf("remote = %+v", remote)
	}

	if err := c.Set(ctx, domain.Variable{Key: "S", Value: "pw", Kind: domain.KindSecret}); err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(secretBody["encrypted_value"])
	if plain, ok := box.OpenAnonymous(nil, raw, pub, priv); !ok || string(plain) != "pw" || secretBody["key_id"] != "k1" {
		t.Fatalf("bad secret body %v", secretBody)
	}

	if err := c.Set(ctx, domain.Variable{Key: "NEW", Value: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1] != "post" {
		t.Fatalf("calls = %v", calls)
	}
}
