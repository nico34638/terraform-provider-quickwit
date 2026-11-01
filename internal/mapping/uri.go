package mapping

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// uriProtocols maps the protocols accepted by Quickwit (Protocol::from_str in
// quickwit-common/src/uri.rs) to their canonical spelling.
var uriProtocols = map[string]string{
	"actor": "actor", "azure": "azure", "file": "file", "grpc": "grpc", "gs": "gs",
	"pg": "postgresql", "postgres": "postgresql", "postgresql": "postgresql",
	"ram": "ram", "s3": "s3",
}

// NormalizeIndexURI returns the URI Quickwit stores for s (Uri::from_str):
// the protocol in its canonical spelling, `file://` added to bare paths, and
// file paths cleaned (`.`, `..`, repeated and trailing slashes). Other
// protocols keep their path as is.
//
// Relative file paths and `~` are rejected: Quickwit resolves them against
// the working directory or home of the node, which the provider cannot know,
// so the stored URI could never be predicted.
//
// Like Quickwit, errors never echo a non-file URI: it may hold credentials.
func NormalizeIndexURI(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("empty URI")
	}
	protocol, p := "file", s
	if before, after, ok := strings.Cut(s, "://"); ok {
		canonical, known := uriProtocols[before]
		if !known {
			protocols := make([]string, 0, len(uriProtocols))
			for k := range uriProtocols {
				protocols = append(protocols, k)
			}
			slices.Sort(protocols)
			return "", fmt.Errorf("unknown URI protocol `%s`, expected one of %s", before, strings.Join(protocols, ", "))
		}
		protocol, p = canonical, after
	}
	if protocol != "file" {
		return protocol + "://" + p, nil
	}
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("`%s` is a relative path: Quickwit would resolve it against the working directory (or the home "+
			"directory, for `~`) of the node. Use an absolute path, such as `file:///var/lib/quickwit/indexes/my-index`", s)
	}
	return "file://" + path.Clean(p), nil
}
