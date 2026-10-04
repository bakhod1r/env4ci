package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func TestClientFlow(t *testing.T) {
	var posted variable
	var deleteQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/projects/g%2Fp/variables", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			t.Error("missing token")
		}
		io.WriteString(w, `[
			{"key":"A","value":"1","masked":false,"environment_scope":"production"},
			{"key":"S","value":"secret12","masked":true,"environment_scope":"production"},
			{"key":"OTHER","value":"x","environment_scope":"staging"}]`)
	})
	mux.HandleFunc("PUT /api/v4/projects/g%2Fp/variables/N", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("POST /api/v4/projects/g%2Fp/variables", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&posted)
		w.WriteHeader(201)
	})
	mux.HandleFunc("DELETE /api/v4/projects/g%2Fp/variables/A", func(w http.ResponseWriter, r *http.Request) {
		deleteQuery = r.URL.Query().Get("filter[environment_scope]")
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "tok", Project: "g/p", Environment: "production"}
	ctx := context.Background()

	remote, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remote) != 2 || remote[1].Kind != domain.KindSecret || !remote[1].Known {
		t.Fatalf("remote = %+v", remote)
	}

	if err := c.Set(ctx, domain.Variable{Key: "N", Value: "longvalue", Kind: domain.KindSecret}); err != nil {
		t.Fatal(err)
	}
	if !posted.Masked || posted.EnvironmentScope != "production" {
		t.Fatalf("posted = %+v", posted)
	}

	if err := c.Delete(ctx, "A", domain.KindVariable); err != nil {
		t.Fatal(err)
	}
	if deleteQuery != "production" {
		t.Fatalf("delete scope = %q", deleteQuery)
	}
}
