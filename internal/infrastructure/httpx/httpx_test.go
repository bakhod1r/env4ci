package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient() (*Client, *[]time.Duration) {
	var waits []time.Duration
	c := New()
	c.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	return c, &waits
}

func TestRetriesThenSucceedsAndReplaysBody(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != "payload" {
			t.Errorf("attempt %d body = %q", n.Load(), b)
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "env4ci/") {
			t.Errorf("ua = %q", r.Header.Get("User-Agent"))
		}
		switch n.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
		case 2:
			w.WriteHeader(503)
		default:
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()

	c, waits := testClient()
	req, _ := http.NewRequest(http.MethodPut, srv.URL, strings.NewReader("payload"))
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	if n.Load() != 3 || len(*waits) != 2 || (*waits)[0] != 2*time.Second || (*waits)[1] != time.Second {
		t.Fatalf("calls=%d waits=%v", n.Load(), *waits)
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(422)
	}))
	defer srv.Close()
	c, _ := testClient()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 422 || n.Load() != 1 {
		t.Fatalf("status=%v calls=%d err=%v", resp.StatusCode, n.Load(), err)
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(502)
	}))
	defer srv.Close()
	c, waits := testClient()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, _ := c.Do(req)
	if resp.StatusCode != 502 || n.Load() != 5 || len(*waits) != 4 || (*waits)[3] != 4*time.Second {
		t.Fatalf("status=%d calls=%d waits=%v", resp.StatusCode, n.Load(), *waits)
	}
}

func TestSecondaryRateLimit403(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c, _ := testClient()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	if resp, _ := c.Do(req); resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestContextCancelStopsRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	c := New()
	c.BaseDelay = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if _, err := c.Do(req); err == nil {
		t.Fatal("want ctx error")
	}
}
