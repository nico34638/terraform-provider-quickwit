package mapping

import (
	"strings"
	"testing"
)

// Expected values follow Uri::from_str (quickwit-common/src/uri.rs, v0.9.3).
func TestNormalizeIndexURI(t *testing.T) {
	for in, want := range map[string]string{
		"file:///quickwit/qwdata/indexes/logs":           "file:///quickwit/qwdata/indexes/logs",
		"file:///quickwit/qwdata/indexes/logs/":          "file:///quickwit/qwdata/indexes/logs",
		"file:///quickwit//qwdata/./indexes/tmp/../logs": "file:///quickwit/qwdata/indexes/logs",
		"/var/lib/quickwit/logs":                         "file:///var/lib/quickwit/logs",
		"file:///..":                                     "file:///",
		"s3://bucket/indexes/logs/":                      "s3://bucket/indexes/logs/", // only file paths are cleaned
		"gs://bucket/a/../b":                             "gs://bucket/a/../b",
		"pg://user:pass@host/db":                         "postgresql://user:pass@host/db",
		"postgres://host/db":                             "postgresql://host/db",
		"azure://container/logs":                         "azure://container/logs",
		"ram:///indexes/logs":                            "ram:///indexes/logs",
	} {
		if got, err := NormalizeIndexURI(in); err != nil || got != want {
			t.Errorf("NormalizeIndexURI(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for in, want := range map[string]string{
		"":                     "empty URI",
		"./data":               "relative path",
		"data/logs":            "relative path",
		"~/indexes":            "relative path",
		"file://data/logs":     "relative path",
		"http://host/x":        "unknown URI protocol `http`",
		"S3://bucket/x":        "unknown URI protocol `S3`",
		"ftp://user:secret@h/": "unknown URI protocol `ftp`",
	} {
		_, err := NormalizeIndexURI(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NormalizeIndexURI(%q) error = %v; want it to contain %q", in, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("NormalizeIndexURI(%q) error leaks credentials: %v", in, err)
		}
	}
}
