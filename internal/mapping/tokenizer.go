package mapping

import (
	"encoding/json"
	"fmt"
	"slices"
)

var (
	TokenizerTypes   = []string{"ngram", "regex", "simple", "source_code"}
	TokenizerFilters = []string{"remove_long", "lower_caser", "ascii_folding"}
)

// Tokenizer mirrors TokenizerEntry (name + flattened TokenizerConfig).
type Tokenizer struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Filters []string `json:"filters"`
	// ngram
	MinGram    *int64 `json:"min_gram,omitempty"`
	MaxGram    *int64 `json:"max_gram,omitempty"`
	PrefixOnly *bool  `json:"prefix_only,omitempty"`
	// regex
	Pattern *string `json:"pattern,omitempty"`
}

// CanonicalizeTokenizer applies Quickwit's defaults to a tokenizer JSON
// document and returns its canonical encoding.
func CanonicalizeTokenizer(raw string) (string, error) {
	t, err := ParseTokenizer(raw)
	if err != nil {
		return "", err
	}
	b, err := sortedJSON(t)
	return string(b), err
}

func ParseTokenizer(raw string) (*Tokenizer, error) {
	var t Tokenizer
	if err := decodeStrict([]byte(raw), &t); err != nil {
		return nil, fmt.Errorf("invalid tokenizer: %w", err)
	}
	if t.Name == "" {
		return nil, fmt.Errorf("tokenizer: missing `name`")
	}
	if t.Filters == nil {
		t.Filters = []string{}
	}
	for _, f := range t.Filters {
		if !slices.Contains(TokenizerFilters, f) {
			return nil, fmt.Errorf("tokenizer `%s`: unknown filter `%s`, expected one of %v", t.Name, f, TokenizerFilters)
		}
	}
	ngramSet := t.MinGram != nil || t.MaxGram != nil || t.PrefixOnly != nil
	switch t.Type {
	case "ngram":
		if t.MinGram == nil || t.MaxGram == nil {
			return nil, fmt.Errorf("tokenizer `%s`: ngram requires `min_gram` and `max_gram`", t.Name)
		}
		if *t.MinGram < 1 || *t.MinGram > *t.MaxGram {
			return nil, fmt.Errorf("tokenizer `%s`: invalid ngram tokenizer, expected 1 <= min_gram <= max_gram", t.Name)
		}
		setDefault(&t.PrefixOnly, false)
		if t.Pattern != nil {
			return nil, fmt.Errorf("tokenizer `%s`: `pattern` is only allowed for regex tokenizers", t.Name)
		}
	case "regex":
		if t.Pattern == nil || *t.Pattern == "" {
			return nil, fmt.Errorf("tokenizer `%s`: regex requires `pattern`", t.Name)
		}
		if ngramSet {
			return nil, fmt.Errorf("tokenizer `%s`: `min_gram`, `max_gram` and `prefix_only` are only allowed for ngram tokenizers", t.Name)
		}
	case "simple", "source_code":
		if ngramSet || t.Pattern != nil {
			return nil, fmt.Errorf("tokenizer `%s`: type `%s` takes no options besides `filters`", t.Name, t.Type)
		}
	default:
		return nil, fmt.Errorf("tokenizer `%s`: unknown type `%s`, expected one of %v", t.Name, t.Type, TokenizerTypes)
	}
	return &t, nil
}

// MustJSON is a helper for tests and defaults.
func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
