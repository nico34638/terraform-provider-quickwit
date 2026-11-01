// Package client is a minimal hand-written client for the Quickwit REST API
// (/api/v1). Index configs are passed around as raw JSON so that the
// provider controls exactly what is sent.
package client

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
	"time"
)

type Client struct {
	baseURL *url.URL
	headers map[string]string
	http    *http.Client
}

type Config struct {
	Endpoint string
	Headers  map[string]string
	Timeout  time.Duration
}

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(cfg.Endpoint, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q: expected an absolute URL such as http://localhost:7280", cfg.Endpoint)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Client{baseURL: u, headers: cfg.Headers, http: &http.Client{Timeout: cfg.Timeout}}, nil
}

// APIError is a non-2xx response. Quickwit returns {"message": "..."}.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("quickwit API error (HTTP %d): %s", e.StatusCode, e.Message)
}

func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	u := c.baseURL.JoinPath("api", "v1", path)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: reading response: %w", method, u, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(respBody, &e) != nil || e.Message == "" {
			e.Message = strings.TrimSpace(string(respBody))
		}
		return &APIError{StatusCode: resp.StatusCode, Message: e.Message}
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("%s %s: decoding response: %w", method, u, err)
		}
	}
	return nil
}

// IndexMetadata is the subset of VersionedIndexMetadata the provider uses.
type IndexMetadata struct {
	IndexUID        string            `json:"index_uid"`
	IndexConfig     json.RawMessage   `json:"index_config"`
	CreateTimestamp int64             `json:"create_timestamp"`
	Sources         []json.RawMessage `json:"sources"`
}

func (c *Client) GetIndex(ctx context.Context, indexID string) (*IndexMetadata, error) {
	var m IndexMetadata
	if err := c.do(ctx, http.MethodGet, "indexes/"+url.PathEscape(indexID), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) CreateIndex(ctx context.Context, config any) (*IndexMetadata, error) {
	var m IndexMetadata
	if err := c.do(ctx, http.MethodPost, "indexes", config, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateIndex replaces the whole index config (PUT semantics).
func (c *Client) UpdateIndex(ctx context.Context, indexID string, config any) (*IndexMetadata, error) {
	var m IndexMetadata
	if err := c.do(ctx, http.MethodPut, "indexes/"+url.PathEscape(indexID), config, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) DeleteIndex(ctx context.Context, indexID string) error {
	return c.do(ctx, http.MethodDelete, "indexes/"+url.PathEscape(indexID), nil, nil)
}
