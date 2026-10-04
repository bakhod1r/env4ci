// Package github implements the Provider port for GitHub Actions secrets and
// variables, at repository or environment level.
package github

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/crypto/nacl/box"

	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/httpx"
)

const DefaultBaseURL = "https://api.github.com"

type Client struct {
	BaseURL     string
	Token       string
	Repo        string // owner/name
	Environment string // empty = repository level
	HTTP        Doer   // nil = httpx.New()

	publicKey *publicKey // fetched once per Client
}

type publicKey struct {
	KeyID string `json:"key_id"`
	Key   string `json:"key"`
}

// MaxSecretSize is GitHub's limit for one secret value.
const MaxSecretSize = 48 * 1024

func (c *Client) Name() string {
	if c.Environment != "" {
		return "github:" + c.Repo + "@" + c.Environment
	}
	return "github:" + c.Repo
}

func (c *Client) prefix() string {
	p := "/repos/" + c.Repo
	if c.Environment != "" {
		return p + "/environments/" + url.PathEscape(c.Environment)
	}
	return p + "/actions"
}

func (c *Client) List(ctx context.Context) ([]domain.Remote, error) {
	var out []domain.Remote
	for page := 1; ; page++ {
		var body struct {
			Total   int `json:"total_count"`
			Secrets []struct {
				Name string `json:"name"`
			} `json:"secrets"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/secrets?per_page=100&page=%d", c.prefix(), page), nil, &body); err != nil {
			return nil, err
		}
		for _, s := range body.Secrets {
			out = append(out, domain.Remote{Key: s.Name, Kind: domain.KindSecret})
		}
		if len(body.Secrets) == 0 || page*100 >= body.Total {
			break
		}
	}
	for page := 1; ; page++ {
		var body struct {
			Total     int `json:"total_count"`
			Variables []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"variables"`
		}
		// GitHub caps variables listing at 30 per page.
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/variables?per_page=30&page=%d", c.prefix(), page), nil, &body); err != nil {
			return nil, err
		}
		for _, v := range body.Variables {
			out = append(out, domain.Remote{Key: v.Name, Value: v.Value, Kind: domain.KindVariable, Known: true})
		}
		if len(body.Variables) == 0 || page*30 >= body.Total {
			break
		}
	}
	return out, nil
}

func (c *Client) Set(ctx context.Context, v domain.Variable) error {
	if v.Kind == domain.KindSecret {
		return c.setSecret(ctx, v)
	}
	path := c.prefix() + "/variables/" + url.PathEscape(v.Key)
	err := c.do(ctx, http.MethodPatch, path, map[string]string{"name": v.Key, "value": v.Value}, nil)
	if isNotFound(err) {
		return c.do(ctx, http.MethodPost, c.prefix()+"/variables", map[string]string{"name": v.Key, "value": v.Value}, nil)
	}
	return err
}

// Validate implements application.Validator.
func (c *Client) Validate(v domain.Variable) error {
	if v.Kind == domain.KindSecret && len(v.Value) > MaxSecretSize {
		return fmt.Errorf("github: secret %s is %d bytes; limit is %d", v.Key, len(v.Value), MaxSecretSize)
	}
	return nil
}

func (c *Client) setSecret(ctx context.Context, v domain.Variable) error {
	if err := c.Validate(v); err != nil {
		return err
	}
	if c.publicKey == nil {
		var pk publicKey
		if err := c.do(ctx, http.MethodGet, c.prefix()+"/secrets/public-key", nil, &pk); err != nil {
			return err
		}
		c.publicKey = &pk
	}
	pk := c.publicKey
	sealed, err := Seal(pk.Key, v.Value)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, c.prefix()+"/secrets/"+url.PathEscape(v.Key),
		map[string]string{"encrypted_value": sealed, "key_id": pk.KeyID}, nil)
}

// Seal encrypts value with the repository public key (libsodium sealed box).
func Seal(publicKeyB64, value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("invalid GitHub public key")
	}
	var pk [32]byte
	copy(pk[:], raw)
	out, err := box.SealAnonymous(nil, []byte(value), &pk, rand.Reader)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func (c *Client) Delete(ctx context.Context, key string, kind domain.Kind) error {
	store := "/variables/"
	if kind == domain.KindSecret {
		store = "/secrets/"
	}
	return c.do(ctx, http.MethodDelete, c.prefix()+store+url.PathEscape(key), nil, nil)
}

// Doer sends HTTP requests; *httpx.Client and *http.Client satisfy it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Msg) }

func isNotFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
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
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		return &apiError{Status: resp.StatusCode, Msg: e.Message}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
