package jenkins

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
	const store = "/job/team/job/app/credentials/store/folder/domain/_"
	var created, deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+store+"/api/json", func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "bob" || p != "tok" {
			t.Error("missing basic auth")
		}
		io.WriteString(w, `{"credentials":[{"id":"DB_PASSWORD"},{"id":"API_KEY"}]}`)
	})
	mux.HandleFunc("POST "+store+"/credential/NEW/updateSubmit", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	mux.HandleFunc("POST "+store+"/createCredentials", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		var body struct {
			Credentials map[string]string `json:"credentials"`
		}
		json.Unmarshal([]byte(r.PostForm.Get("json")), &body)
		created = body.Credentials["id"] + "=" + body.Credentials["secret"] + " " + body.Credentials["$class"]
		w.Header().Set("Location", "/")
		w.WriteHeader(302)
	})
	mux.HandleFunc("POST "+store+"/credential/DB_PASSWORD/updateSubmit", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(302)
	})
	mux.HandleFunc("POST "+store+"/credential/API_KEY/doDelete", func(w http.ResponseWriter, _ *http.Request) {
		deleted = "API_KEY"
		w.WriteHeader(302)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// Redirects must not be followed: the test server would 404 on "/".
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c := &Client{BaseURL: srv.URL, User: "bob", Token: "tok", Folder: "team/app", HTTP: hc}
	ctx := context.Background()

	remote, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remote) != 2 || remote[0].Known || remote[0].Kind != domain.KindSecret {
		t.Fatalf("remote = %+v", remote)
	}
	if err := c.Set(ctx, domain.Variable{Key: "NEW", Value: "v1", Kind: domain.KindVariable}); err != nil {
		t.Fatal(err)
	}
	if created != "NEW=v1 "+stringCredentials {
		t.Fatalf("created = %q", created)
	}
	if err := c.Set(ctx, domain.Variable{Key: "DB_PASSWORD", Value: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "API_KEY", domain.KindSecret); err != nil || deleted != "API_KEY" {
		t.Fatalf("delete: %v %q", err, deleted)
	}
	if c.Name() != "jenkins:team/app" || (&Client{}).Name() != "jenkins:system" {
		t.Fatal("name")
	}
}

func TestSystemStoreAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credentials/store/system/domain/_/api/json" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.WriteHeader(403)
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL, HTTP: srv.Client()}).List(context.Background())
	if err == nil || err.Error() != "jenkins: HTTP 403: Forbidden" {
		t.Fatalf("err = %v", err)
	}
}

func TestTransportErrors(t *testing.T) {
	c := &Client{BaseURL: "://bad"}
	c.KindAgnostic()
	if _, err := c.List(context.Background()); err == nil {
		t.Fatal("want bad URL error")
	}
	// Default client, closed port: the request itself fails.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c = &Client{BaseURL: srv.URL}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Delete(ctx, "X", domain.KindSecret); err == nil {
		t.Fatal("want transport error")
	}
}

func TestPostOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	if err := (&Client{BaseURL: srv.URL, HTTP: srv.Client()}).Set(context.Background(), domain.Variable{Key: "A", Value: "1"}); err != nil {
		t.Fatal(err)
	}
}
