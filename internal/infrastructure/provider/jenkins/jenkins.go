// Package jenkins implements the Provider port for Jenkins credentials:
// every key is a "Secret text" credential whose ID is the key, in the
// system store or a folder's store. Jenkins never returns secret values, so
// listed credentials are unverifiable and pull yields names only.
package jenkins

import (
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

const stringCredentials = "org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl" // #nosec G101 -- a Java class name, not a credential

type Client struct {
	BaseURL string // https://jenkins.example.com
	User    string
	Token   string // API token
	Folder  string // "" = system store; "team/app" = that folder's store
	HTTP    Doer   // nil = httpx.New()
}

// KindAgnostic: Jenkins stores every value as secret text.
func (c *Client) KindAgnostic() {}

func (c *Client) Name() string {
	if c.Folder == "" {
		return "jenkins:system"
	}
	return "jenkins:" + c.Folder
}

// store is the credentials domain path ("_" is the global domain).
func (c *Client) store() string {
	if c.Folder == "" {
		return "/credentials/store/system/domain/_"
	}
	var b strings.Builder
	for _, part := range strings.Split(strings.Trim(c.Folder, "/"), "/") {
		b.WriteString("/job/" + url.PathEscape(part))
	}
	return b.String() + "/credentials/store/folder/domain/_"
}

func (c *Client) credential(id string) string { return c.store() + "/credential/" + url.PathEscape(id) }

func (c *Client) List(ctx context.Context) ([]domain.Remote, error) {
	var body struct {
		Credentials []struct {
			ID string `json:"id"`
		} `json:"credentials"`
	}
	if err := c.do(ctx, http.MethodGet, c.store()+"/api/json?tree=credentials[id]", nil, &body); err != nil {
		return nil, err
	}
	out := make([]domain.Remote, 0, len(body.Credentials))
	for _, cr := range body.Credentials {
		out = append(out, domain.Remote{Key: cr.ID, Kind: domain.KindSecret})
	}
	return out, nil
}

// form is the createCredentials / updateSubmit payload.
func form(v domain.Variable) url.Values {
	b, _ := json.Marshal(map[string]any{
		"": "0",
		"credentials": map[string]string{
			"scope":       "GLOBAL",
			"id":          v.Key,
			"secret":      v.Value,
			"description": "managed by env4ci",
			"$class":      stringCredentials,
		},
	})
	return url.Values{"json": {string(b)}}
}

func (c *Client) Set(ctx context.Context, v domain.Variable) error {
	err := c.do(ctx, http.MethodPost, c.credential(v.Key)+"/updateSubmit", form(v), nil)
	if isNotFound(err) {
		return c.do(ctx, http.MethodPost, c.store()+"/createCredentials", form(v), nil)
	}
	return err
}

func (c *Client) Delete(ctx context.Context, key string, _ domain.Kind) error {
	return c.do(ctx, http.MethodPost, c.credential(key)+"/doDelete", url.Values{}, nil)
}

// Doer sends HTTP requests; *httpx.Client and *http.Client satisfy it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("jenkins: HTTP %d: %s", e.Status, e.Msg) }

func isNotFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, in url.Values, out any) error {
	var body io.Reader
	if in != nil {
		body = strings.NewReader(in.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	// An API token needs no CSRF crumb.
	req.SetBasicAuth(c.User, c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.HTTP == nil {
		c.HTTP = httpx.New()
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && method == http.MethodPost {
		return nil // form posts answer with a redirect on success
	}
	if resp.StatusCode >= 300 {
		// Only the status line: Jenkins error pages are HTML and may echo the form.
		return &apiError{Status: resp.StatusCode, Msg: http.StatusText(resp.StatusCode)}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
