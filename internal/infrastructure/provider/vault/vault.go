// Package vault implements the Provider port for HashiCorp Vault KV v2.
// All keys of one environment live in one secret (mount/path); a push is a
// single check-and-set write, so it creates exactly one new version.
package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/httpx"
)

type Client struct {
	Address   string // VAULT_ADDR, e.g. https://vault.example.com:8200
	Token     string // VAULT_TOKEN
	Namespace string // VAULT_NAMESPACE (Vault Enterprise / HCP)
	Mount     string // KV v2 mount, default "secret"
	Path      string // secret path inside the mount, e.g. myapp/production
	HTTP      Doer   // nil = httpx.New()
}

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// KindAgnostic: Vault has no secret/variable distinction.
func (c *Client) KindAgnostic() {}

func (c *Client) Name() string { return "vault:" + c.mount() + "/" + strings.Trim(c.Path, "/") }

func (c *Client) mount() string {
	if c.Mount == "" {
		return "secret"
	}
	return strings.Trim(c.Mount, "/")
}

func (c *Client) dataURL() string {
	return strings.TrimRight(c.Address, "/") + "/v1/" + c.mount() + "/data/" + strings.Trim(c.Path, "/")
}

// read returns current data and version; version 0 means the secret does not exist.
func (c *Client) read(ctx context.Context) (map[string]string, int, error) {
	var body struct {
		Data struct {
			Data     map[string]any `json:"data"`
			Metadata struct {
				Version int `json:"version"`
			} `json:"metadata"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, nil, &body)
	if isNotFound(err) {
		return map[string]string{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	out := make(map[string]string, len(body.Data.Data))
	for k, v := range body.Data.Data {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		default:
			b, _ := json.Marshal(t) // numbers, bools, objects written by other tools
			out[k] = string(b)
		}
	}
	return out, body.Data.Metadata.Version, nil
}

func (c *Client) List(ctx context.Context) ([]domain.Remote, error) {
	data, _, err := c.read(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]domain.Remote, 0, len(keys))
	for _, k := range keys {
		out = append(out, domain.Remote{Key: k, Value: data[k], Kind: domain.KindSecret, Known: true})
	}
	return out, nil
}

// ApplyBatch merges set/del into the secret with check-and-set, so a
// concurrent writer makes the push fail instead of being overwritten.
func (c *Client) ApplyBatch(ctx context.Context, set []domain.Variable, del []string) error {
	data, version, err := c.read(ctx)
	if err != nil {
		return err
	}
	for _, v := range set {
		data[v.Key] = v.Value
	}
	for _, k := range del {
		delete(data, k)
	}
	payload := map[string]any{"data": data, "options": map[string]int{"cas": version}}
	err = c.do(ctx, http.MethodPost, payload, nil)
	var e *apiError
	if errors.As(err, &e) && e.Status == http.StatusBadRequest && strings.Contains(e.Msg, "check-and-set") {
		return fmt.Errorf("vault: %s changed during push (version %d); run diff again", c.Path, version)
	}
	return err
}

func (c *Client) Set(ctx context.Context, v domain.Variable) error {
	return c.ApplyBatch(ctx, []domain.Variable{v}, nil)
}

func (c *Client) Delete(ctx context.Context, key string, _ domain.Kind) error {
	return c.ApplyBatch(ctx, nil, []string{key})
}

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("vault: HTTP %d: %s", e.Status, e.Msg) }

func isNotFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.dataURL(), body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", c.Token)
	req.Header.Set("X-Vault-Request", "true")
	if c.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", c.Namespace)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.HTTP == nil {
		c.HTTP = httpx.New()
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		// Vault errors list messages only; request values are never echoed.
		var e struct {
			Errors []string `json:"errors"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		return &apiError{Status: resp.StatusCode, Msg: strings.Join(e.Errors, "; ")}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
