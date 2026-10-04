package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (errReader) Close() error             { return nil }

func TestBodyReadError(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1", nil)
	req.Body = errReader{}
	if _, err := New().Do(req); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultsAndNetworkErrorRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	addr := srv.URL
	srv.Close() // connection refused from now on

	c := &Client{MaxRetries: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond} // HTTP nil -> default client
	var slept int
	c.sleep = func(context.Context, time.Duration) error { slept++; return nil }
	req, _ := http.NewRequest(http.MethodGet, addr, nil)
	if _, err := c.Do(req); err == nil || slept != 1 {
		t.Fatalf("err=%v slept=%d", err, slept)
	}
}

func TestBackoffRateLimitReset(t *testing.T) {
	c := New()
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("X-RateLimit-Remaining", "0")
	resp.Header.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(5*time.Second).Unix(), 10))
	if d := c.backoff(0, resp); d <= 0 || d > 6*time.Second {
		t.Fatalf("d = %v", d)
	}
	resp.Header.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10))
	if d := c.backoff(0, resp); d != 0 {
		t.Fatalf("past reset: %v", d)
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
