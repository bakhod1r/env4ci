// Package httpx is the HTTP client shared by providers: request timeout,
// User-Agent, and retries on rate limits and transient server errors.
package httpx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"time"
)

// UserAgent is sent on every request; main sets the version.
var UserAgent = "env4ci/dev"

// Client retries idempotent-safe failures: network errors, 429, 502, 503, 504.
// Every provider call (GET/PUT/PATCH/DELETE, and POST create guarded by a
// prior 404) is safe to repeat.
type Client struct {
	HTTP       *http.Client
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	sleep      func(context.Context, time.Duration) error
}

func New() *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   30 * time.Second,
	}
}

// Do sends req, buffering the body so it can be replayed.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	sleep := c.sleep
	if sleep == nil {
		sleep = sleepCtx
	}

	for attempt := 0; ; attempt++ {
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		resp, err := hc.Do(req)
		if attempt >= c.MaxRetries || !retryable(resp, err) || req.Context().Err() != nil {
			return resp, err
		}
		wait := c.backoff(attempt, resp)
		if resp != nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
		}
		if err := sleep(req.Context(), wait); err != nil {
			return nil, err
		}
	}
}

func retryable(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusForbidden: // GitHub secondary rate limit
		return resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0"
	}
	return false
}

func (c *Client) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
			return min(time.Duration(s)*time.Second, c.MaxDelay)
		}
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return min(max(time.Until(time.Unix(reset, 0)), 0), c.MaxDelay)
		}
	}
	return min(c.BaseDelay<<attempt, c.MaxDelay)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
