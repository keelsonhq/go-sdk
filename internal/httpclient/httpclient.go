// Package httpclient provides shared HTTP helpers for all Keelson SDK packages.
package httpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client wraps an http.Client with base URL and auth token.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// New creates a Client. If httpClient is nil, http.DefaultClient is used.
// Trailing slashes on baseURL are stripped to avoid double-slash in paths.
func New(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		HTTPClient: httpClient,
	}
}

// APIError represents an HTTP error from the Keelson API.
type APIError struct {
	StatusCode int
	Body       string
	Method     string
	URL        string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s failed with %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
}

// DoJSONWithHeaders is like DoJSON but allows setting additional headers.
// It is used by User-Context APIs that forward cookie/authorization/host
// instead of using bearer tokens.
func (c *Client) DoJSONWithHeaders(method, path string, body io.Reader, dest any, headers map[string]string) error {
	url := c.BaseURL + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		if http.CanonicalHeaderKey(k) == "Host" {
			// Go's net/http uses req.Host, not the Header map.
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			Method:     method,
			URL:        url,
		}
	}

	if dest != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, dest); err != nil {
			return fmt.Errorf("decode JSON: %w", err)
		}
	}

	return nil
}

// DoRaw sends an HTTP request and returns the raw *http.Response.
// The caller is responsible for closing resp.Body.
// Non-2xx status codes are returned as *APIError (body is consumed and resp is nil).
func (c *Client) DoRaw(method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	return c.DoRawCtx(context.Background(), method, path, body, headers)
}

// DoRawCtx is like DoRaw but accepts a context for cancellation and timeouts.
func (c *Client) DoRawCtx(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for k, v := range headers {
		if http.CanonicalHeaderKey(k) == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			Method:     method,
			URL:        url,
		}
	}

	return resp, nil
}

// DoJSON sends an HTTP request and decodes the JSON response into dest.
func (c *Client) DoJSON(method, path string, body io.Reader, dest any) error {
	return c.DoJSONCtx(context.Background(), method, path, body, dest)
}

// DoJSONCtx is like DoJSON but accepts a context for cancellation and timeouts.
func (c *Client) DoJSONCtx(ctx context.Context, method, path string, body io.Reader, dest any) error {
	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			Method:     method,
			URL:        url,
		}
	}

	if dest != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, dest); err != nil {
			return fmt.Errorf("decode JSON: %w", err)
		}
	}

	return nil
}
