package gitlab

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bakhod1r/env4ci/internal/domain"
)

func resp(code int, body string) doerFunc {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	}
}

func TestNameValidateError(t *testing.T) {
	c := &Client{Project: "g/p"}
	if c.Name() != "gitlab:g/p@*" {
		t.Fatalf("%q", c.Name())
	}
	if err := c.Validate(domain.Variable{Key: "V", Value: "x"}); err != nil {
		t.Fatal("variables are not masked")
	}
	if err := c.Validate(domain.Variable{Key: "S", Value: "x", Kind: domain.KindSecret}); err == nil {
		t.Fatal("short secret must fail")
	}
	if (&apiError{Status: 400, Msg: "m"}).Error() != "gitlab: HTTP 400: m" {
		t.Fatal("Error()")
	}
}

func TestListAndSetErrors(t *testing.T) {
	ctx := context.Background()
	c := &Client{Project: "p", HTTP: resp(401, `{"message":"401 Unauthorized"}`)}
	if _, err := c.List(ctx); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("list: %v", err)
	}
	if err := c.Set(ctx, domain.Variable{Key: "V", Value: "x"}); err == nil {
		t.Fatal("set: want error")
	}
}

func TestPagination(t *testing.T) {
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Query().Get("page") == "1" {
			var b strings.Builder
			b.WriteString("[")
			for i := 0; i < 100; i++ {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(`{"key":"K` + string(rune('A'+i%26)) + `","value":"v","environment_scope":"*"}`)
			}
			b.WriteString("]")
			io.WriteString(w, b.String())
			return
		}
		io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	rs, err := (&Client{BaseURL: srv.URL, Project: "p"}).List(context.Background())
	if err != nil || pages != 2 || len(rs) != 100 {
		t.Fatalf("pages=%d len=%d err=%v", pages, len(rs), err)
	}
}

func TestDoErrors(t *testing.T) {
	ctx := context.Background()
	c := &Client{Project: "p"}
	if err := c.do(ctx, http.MethodPost, "/x", make(chan int), nil); err == nil {
		t.Fatal("marshal")
	}
	c.BaseURL = "http://bad\x7fhost"
	if err := c.do(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("request")
	}
	c = &Client{Project: "p", HTTP: doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("dial") })}
	if err := c.do(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("transport")
	}
	var urls []string
	c = &Client{Project: "p", HTTP: doerFunc(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		return resp(200, "{bad")(r)
	})}
	var out any
	if err := c.do(ctx, http.MethodGet, "/x", nil, &out); err == nil {
		t.Fatal("decode")
	}
	if urls[0] != "https://gitlab.com/x" {
		t.Fatalf("default base: %v", urls)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	c = &Client{Project: "p", BaseURL: srv.URL}
	if err := c.do(ctx, http.MethodGet, "/x", nil, nil); err != nil || c.HTTP == nil {
		t.Fatalf("default client: %v", err)
	}
}
