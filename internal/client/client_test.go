package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, endpoint string, cfg Config) *Client {
	t.Helper()
	cfg.Endpoint = endpoint
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUserAgentAndHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"index_uid": "x:1", "index_config": {}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, Config{UserAgent: "Terraform/1.13 terraform-provider-quickwit/test", Headers: map[string]string{"X-Token": "secret"}})
	if _, err := c.GetIndex(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if ua := got.Get("User-Agent"); ua != "Terraform/1.13 terraform-provider-quickwit/test" {
		t.Errorf("User-Agent = %q", ua)
	}
	if v := got.Get("X-Token"); v != "secret" {
		t.Errorf("X-Token = %q", v)
	}

	// User headers take precedence.
	c = newTestClient(t, srv.URL, Config{UserAgent: "default", Headers: map[string]string{"User-Agent": "custom"}})
	if _, err := c.GetIndex(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if ua := got.Get("User-Agent"); ua != "custom" {
		t.Errorf("User-Agent = %q, want the header from the configuration", ua)
	}
}

func TestAPIErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"quickwit message": {404, `{"message": "index ` + "`x`" + ` not found"}`, "index `x` not found"},
		"empty body":       {502, "", "Bad Gateway"},
		"long html page":   {503, "<html>" + strings.Repeat("a", 2000) + "</html>", "… (truncated)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := newTestClient(t, srv.URL, Config{}).GetIndex(context.Background(), "x")
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(err.Error()) > 700 {
				t.Errorf("error = %v; want it to contain %q (and be short)", err, tc.want)
			}
			if IsNotFound(err) != (tc.status == 404) {
				t.Errorf("IsNotFound = %v for HTTP %d", IsNotFound(err), tc.status)
			}
		})
	}
}

func TestTransportErrorRedactsPassword(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	endpoint := strings.Replace(srv.URL, "http://", "http://user:hunter2@", 1)
	srv.Close() // connection refused

	c := newTestClient(t, endpoint, Config{})
	_, err := c.GetIndex(context.Background(), "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if strings.Contains(msg, "hunter2") {
		t.Errorf("error leaks the password: %s", msg)
	}
	if strings.Count(msg, "/api/v1/indexes/x") != 1 {
		t.Errorf("the URL should appear exactly once: %s", msg)
	}
	if c.Endpoint() != strings.Replace(endpoint, "hunter2", "xxxxx", 1) {
		t.Errorf("Endpoint() = %q", c.Endpoint())
	}
}
