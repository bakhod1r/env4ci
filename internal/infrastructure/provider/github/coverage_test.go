package github

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"

	"github.com/bakhod1r/env4ci/internal/domain"
)

type recordDoer struct {
	urls []string
	resp func(*http.Request) (*http.Response, error)
}

func (r *recordDoer) Do(req *http.Request) (*http.Response, error) {
	r.urls = append(r.urls, req.Method+" "+req.URL.String())
	return r.resp(req)
}

func jsonResp(code int, body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	}
}

func TestNameAndRepoLevelPrefix(t *testing.T) {
	if (&Client{Repo: "o/r"}).Name() != "github:o/r" || (&Client{Repo: "o/r", Environment: "prod"}).Name() != "github:o/r@prod" {
		t.Fatal("Name")
	}
	d := &recordDoer{resp: jsonResp(204, "")}
	c := &Client{Repo: "o/r", Token: "t", HTTP: d} // BaseURL empty -> api.github.com, never actually dialed
	if err := c.Delete(context.Background(), "S", domain.KindSecret); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "V", domain.KindVariable); err != nil {
		t.Fatal(err)
	}
	want := []string{"DELETE https://api.github.com/repos/o/r/actions/secrets/S", "DELETE https://api.github.com/repos/o/r/actions/variables/V"}
	if strings.Join(d.urls, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", d.urls)
	}
}

func TestListErrors(t *testing.T) {
	ctx := context.Background()
	c := &Client{Repo: "o/r", HTTP: &recordDoer{resp: jsonResp(500, `{"message":"boom"}`)}}
	if _, err := c.List(ctx); err == nil || !strings.Contains(err.Error(), "HTTP 500: boom") {
		t.Fatalf("secrets: %v", err)
	}
	calls := 0
	c.HTTP = &recordDoer{resp: func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(r.URL.Path, "/secrets") {
			return jsonResp(200, `{"total_count":0,"secrets":[]}`)(r)
		}
		return jsonResp(403, `{"message":"no"}`)(r)
	}}
	if _, err := c.List(ctx); err == nil || calls != 2 {
		t.Fatalf("variables: %v calls=%d", err, calls)
	}
}

func TestSetErrors(t *testing.T) {
	ctx := context.Background()
	c := &Client{Repo: "o/r", HTTP: &recordDoer{resp: jsonResp(422, `{"message":"bad"}`)}}
	if err := c.Set(ctx, domain.Variable{Key: "V", Value: "1"}); err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("patch: %v", err)
	}
	if err := c.Set(ctx, domain.Variable{Key: "S", Value: "1", Kind: domain.KindSecret}); err == nil {
		t.Fatal("public key: want error")
	}
	c = &Client{Repo: "o/r", HTTP: &recordDoer{resp: jsonResp(200, `{"key_id":"k","key":"not-base64!"}`)}}
	if err := c.Set(ctx, domain.Variable{Key: "S", Value: "1", Kind: domain.KindSecret}); err == nil || !strings.Contains(err.Error(), "invalid GitHub public key") {
		t.Fatalf("seal: %v", err)
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestSealRandFailure(t *testing.T) {
	pub, _, _ := box.GenerateKey(rand.Reader)
	old := randReader
	randReader = failReader{}
	defer func() { randReader = old }()
	if _, err := Seal(base64.StdEncoding.EncodeToString(pub[:]), "x"); err == nil {
		t.Fatal("want error")
	}
}

func TestDoErrors(t *testing.T) {
	ctx := context.Background()
	c := &Client{Repo: "o/r"}
	if err := c.do(ctx, http.MethodPost, "/x", make(chan int), nil); err == nil {
		t.Fatal("marshal: want error")
	}
	c.BaseURL = "http://bad\x7fhost"
	if err := c.do(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("request: want error")
	}
	c = &Client{Repo: "o/r", HTTP: &recordDoer{resp: func(*http.Request) (*http.Response, error) { return nil, errors.New("dial") }}}
	if err := c.do(ctx, http.MethodGet, "/x", nil, nil); err == nil || err.Error() != "dial" {
		t.Fatalf("transport: %v", err)
	}
	c.HTTP = &recordDoer{resp: jsonResp(200, "{not json")}
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/x", nil, &out); err == nil {
		t.Fatal("decode: want error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	c = &Client{Repo: "o/r", BaseURL: srv.URL} // HTTP nil -> httpx default
	if err := c.do(ctx, http.MethodGet, "/x", nil, nil); err != nil || c.HTTP == nil {
		t.Fatalf("default client: %v", err)
	}
}
