// Package gitlab implements the Provider port for GitLab CI/CD project
// variables. Secrets map to masked variables; plain variables to unmasked ones.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/bakhod1r/env4ci/internal/domain"
	"github.com/bakhod1r/env4ci/internal/infrastructure/httpx"
)

const DefaultBaseURL = "https://gitlab.com"

type Client struct {
	BaseURL     string
	Token       string
	Project     string // numeric id or group/project path
	Environment string // environment_scope; empty = "*"
	Protected   bool
	HTTP        Doer // nil = httpx.New()
}

func (c *Client) Name() string { return "gitlab:" + c.Project + "@" + c.scope() }

func (c *Client) scope() string {
	if c.Environment == "" {
		return "*"
	}
	return c.Environment
}

func (c *Client) base() string { return "/api/v4/projects/" + url.PathEscape(c.Project) + "/variables" }

func (c *Client) keyPath(key string) string {
	return c.base() + "/" + url.PathEscape(key) + "?filter[environment_scope]=" + url.QueryEscape(c.scope())
}

type variable struct {
	Key              string `json:"key"`
	Value            string `json:"value"`
	Masked           bool   `json:"masked"`
	Protected        bool   `json:"protected"`
	EnvironmentScope string `json:"environment_scope"`
}

func (c *Client) List(ctx context.Context) ([]domain.Remote, error) {
	var out []domain.Remote
	for page := 1; ; page++ {
		var vars []variable
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d", c.base(), page), nil, &vars); err != nil {
			return nil, err
		}
		for _, v := range vars {
			if v.EnvironmentScope != c.scope() {
				continue
			}
			kind := domain.KindVariable
			if v.Masked {
				kind = domain.KindSecret
			}
			out = append(out, domain.Remote{Key: v.Key, Value: v.Value, Kind: kind, Known: true})
		}
		if len(vars) < 100 {
			return out, nil
		}
	}
}

// MinMaskedLength is GitLab's minimum length for a masked variable.
const MinMaskedLength = 8

// ValidateMasked reports values GitLab refuses to mask, before any API call.
func ValidateMasked(key, value string) error {
	switch {
	case len(value) < MinMaskedLength:
		return fmt.Errorf("gitlab: secret %s must be at least %d characters to be masked; make it longer or classify it as a variable", key, MinMaskedLength)
	case strings.ContainsAny(value, "\n\r"):
		return fmt.Errorf("gitlab: secret %s is multi-line and cannot be masked; base64-encode it or classify it as a variable", key)
	}
	return nil
}

// Validate implements application.Validator.
func (c *Client) Validate(v domain.Variable) error {
	if v.Kind == domain.KindSecret {
		return ValidateMasked(v.Key, v.Value)
	}
	return nil
}

func (c *Client) Set(ctx context.Context, v domain.Variable) error {
	if v.Kind == domain.KindSecret {
		if err := ValidateMasked(v.Key, v.Value); err != nil {
			return err
		}
	}
	body := variable{
		Key: v.Key, Value: v.Value,
		Masked:           v.Kind == domain.KindSecret,
		Protected:        c.Protected,
		EnvironmentScope: c.scope(),
	}
	err := c.do(ctx, http.MethodPut, c.keyPath(v.Key), body, nil)
	if isNotFound(err) {
		return c.do(ctx, http.MethodPost, c.base(), body, nil)
	}
	return err
}

func (c *Client) Delete(ctx context.Context, key string, _ domain.Kind) error {
	return c.do(ctx, http.MethodDelete, c.keyPath(key), nil, nil)
}

// Doer sends HTTP requests; *httpx.Client and *http.Client satisfy it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("gitlab: HTTP %d: %s", e.Status, e.Msg) }

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
	req.Header.Set("PRIVATE-TOKEN", c.Token)
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
		// GitLab errors never echo variable values, only field messages.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
		return &apiError{Status: resp.StatusCode, Msg: strings.TrimSpace(string(msg))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
