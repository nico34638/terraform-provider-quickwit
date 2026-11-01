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

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	// maxResponseSize bounds the responses read in memory; index metadata is
	// a few hundred KB at most.
	maxResponseSize = 16 << 20
	// maxErrorMessage truncates error bodies that are not Quickwit JSON
	// errors (e.g. an HTML page from a reverse proxy).
	maxErrorMessage = 512
)

type Client struct {
	baseURL   *url.URL
	headers   map[string]string
	userAgent string
	http      *http.Client
}

type Config struct {
	Endpoint string
	Headers  map[string]string
	Timeout  time.Duration
	// UserAgent is sent with every request; Headers can override it.
	UserAgent string
}

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(cfg.Endpoint, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q: expected an absolute URL such as http://localhost:7280", cfg.Endpoint)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Client{baseURL: u, headers: cfg.Headers, userAgent: cfg.UserAgent, http: &http.Client{Timeout: cfg.Timeout}}, nil
}

// Endpoint is the base URL, with any password redacted.
func (c *Client) Endpoint() string { return c.baseURL.Redacted() }

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
			return fmt.Errorf("encoding request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	u := c.baseURL.JoinPath("api", "v1", path)
	// Error messages and logs never show the password of the endpoint URL.
	target := method + " " + u.Redacted()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return fmt.Errorf("%s: %w", target, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	// Header values and bodies are never logged: they hold credentials
	// (proxy tokens, Kafka passwords, Pulsar tokens...).
	fields := map[string]any{"method": method, "url": u.Redacted()}
	tflog.Debug(ctx, "Quickwit API request", fields)
	start := time.Now()
	resp, err := c.http.Do(req)
	fields["duration_ms"] = time.Since(start).Milliseconds()
	if err != nil {
		// *url.Error repeats the method and URL: keep only the cause.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		fields["error"] = err.Error()
		tflog.Debug(ctx, "Quickwit API request failed", fields)
		return fmt.Errorf("%s: %w", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	fields["status"] = resp.StatusCode
	tflog.Debug(ctx, "Quickwit API response", fields)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return fmt.Errorf("%s: reading response: %w", target, err)
	}
	if len(respBody) > maxResponseSize {
		return fmt.Errorf("%s: response larger than %d bytes", target, maxResponseSize)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{StatusCode: resp.StatusCode, Message: errorMessage(resp.StatusCode, respBody)}
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("%s: decoding response: %w", target, err)
		}
	}
	return nil
}

// errorMessage extracts Quickwit's {"message": "..."}; other bodies (proxy
// error pages...) are truncated, empty ones replaced by the status text.
func errorMessage(status int, body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return e.Message
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return http.StatusText(status)
	}
	if len(msg) > maxErrorMessage {
		msg = strings.ToValidUTF8(msg[:maxErrorMessage], "") + "… (truncated)"
	}
	return msg
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
