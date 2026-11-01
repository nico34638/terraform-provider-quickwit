package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Source is a VersionedSourceConfig (version 0.9).
type Source struct {
	Version      string          `json:"version"`
	SourceID     string          `json:"source_id"`
	NumPipelines int64           `json:"num_pipelines"`
	Enabled      bool            `json:"enabled"`
	SourceType   string          `json:"source_type"`
	Params       json.RawMessage `json:"params,omitempty"`
	Transform    *Transform      `json:"transform,omitempty"`
	InputFormat  string          `json:"input_format"`
}

type Transform struct {
	Script   string `json:"script"`
	Timezone string `json:"timezone,omitempty"`
}

func sourcePath(indexID string, sourceID ...string) string {
	p := "indexes/" + url.PathEscape(indexID) + "/sources"
	for _, s := range sourceID {
		p += "/" + url.PathEscape(s)
	}
	return p
}

// CreateSource adds a source. Quickwit checks the source connectivity first.
func (c *Client) CreateSource(ctx context.Context, indexID string, src *Source) (*Source, error) {
	var out Source
	if err := c.do(ctx, http.MethodPost, sourcePath(indexID), src, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetSource(ctx context.Context, indexID, sourceID string) (*Source, error) {
	var out Source
	if err := c.do(ctx, http.MethodGet, sourcePath(indexID, sourceID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSource replaces the whole source config (PUT semantics).
func (c *Client) UpdateSource(ctx context.Context, indexID string, src *Source) (*Source, error) {
	var out Source
	if err := c.do(ctx, http.MethodPut, sourcePath(indexID, src.SourceID), src, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteSource(ctx context.Context, indexID, sourceID string) error {
	return c.do(ctx, http.MethodDelete, sourcePath(indexID, sourceID), nil, nil)
}
