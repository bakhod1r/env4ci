package vault

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

func TestMountDefaultAndKindAgnostic(t *testing.T) {
	c := &Client{Path: "a"}
	c.KindAgnostic()
	if c.Name() != "vault:secret/a" {
		t.Fatalf("%q", c.Name())
	}
}

func TestNullValueAndSetDelete(t *testing.T) {
	kv := &fakeKV{data: map[string]any{"N": nil, "A": "1"}, version: 1}
	c := newClient(t, kv)
	ctx := context.Background()
	rs, err := c.List(ctx)
	if err != nil || len(rs) != 2 || rs[1].Key != "N" || rs[1].Value != "" {
		t.Fatalf("rs=%+v err=%v", rs, err)
	}
	if err := c.Set(ctx, domain.Variable{Key: "B", Value: "2"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "A", domain.KindSecret); err != nil {
		t.Fatal(err)
	}
	if _, ok := kv.data["A"]; ok || kv.data["B"] != "2" {
		t.Fatalf("data = %v", kv.data)
	}
}

func TestApplyBatchReadError(t *testing.T) {
	kv := &fakeKV{}
	c := newClient(t, kv)
	c.Token = "bad"
	if err := c.ApplyBatch(context.Background(), []domain.Variable{{Key: "A"}}, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestDoErrors(t *testing.T) {
	ctx := context.Background()
	c := &Client{Address: "http://x", Path: "p"}
	if err := c.do(ctx, http.MethodPost, make(chan int), nil); err == nil {
		t.Fatal("marshal")
	}
	c.Address = "http://bad\x7fhost"
	if err := c.do(ctx, http.MethodGet, nil, nil); err == nil {
		t.Fatal("request")
	}
	c = &Client{Address: "http://x", Path: "p", HTTP: doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("dial") })}
	if err := c.do(ctx, http.MethodGet, nil, nil); err == nil {
		t.Fatal("transport")
	}
	c.HTTP = doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{bad")), Header: http.Header{}}, nil
	})
	var out any
	if err := c.do(ctx, http.MethodGet, nil, &out); err == nil {
		t.Fatal("decode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	c = &Client{Address: srv.URL, Path: "p"}
	if err := c.do(ctx, http.MethodGet, nil, nil); err != nil || c.HTTP == nil {
		t.Fatalf("default client: %v", err)
	}
}
