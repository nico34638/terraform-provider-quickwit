package mapping_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nico34638/terraform-provider-quickwit/internal/client"
	"github.com/nico34638/terraform-provider-quickwit/internal/mapping"
)

// TestRoundTrip creates an index holding one field of each kind and checks
// that what Quickwit returns is byte-identical to our canonical form. This is
// what guarantees there are no spurious diffs. Requires TF_ACC=1 and a
// running Quickwit (docker compose up -d).
func TestRoundTrip(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run against Quickwit")
	}
	endpoint := os.Getenv("QUICKWIT_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:7280"
	}
	c, err := client.New(client.Config{Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	inputs := []string{
		`{"name":"ts","type":"datetime","fast":true}`,
		`{"name":"ts_ms","type":"datetime","precision":"milliseconds","input_formats":["unix_timestamp"],"output_format":"unix_timestamp_millis"}`,
		`{"name":"t","type":"text"}`,
		`{"name":"t_raw_fast","type":"text","fast":true,"tokenizer":"raw","record":"position","fieldnorms":true}`,
		`{"name":"t_norm","type":"text","fast":{"normalizer":"lowercase"}}`,
		`{"name":"t_noidx","type":"text","indexed":false,"description":"not indexed"}`,
		`{"name":"t_arr","type":"array<text>","stored":false}`,
		`{"name":"n","type":"i64"}`,
		`{"name":"u","type":"array<u64>","coerce":false}`,
		`{"name":"f","type":"f64","fast":true,"output_format":"string"}`,
		`{"name":"b","type":"bool","fast":true}`,
		`{"name":"ip","type":"ip","indexed":false}`,
		`{"name":"by","type":"bytes","fast":true,"input_format":"hex","output_format":"hex"}`,
		`{"name":"by_arr","type":"array<bytes>"}`,
		`{"name":"j","type":"json","expand_dots":false,"fast":true}`,
		`{"name":"j_noidx","type":"json","indexed":false}`,
		`{"name":"o","type":"object","field_mappings":[{"name":"x","type":"text"},{"name":"inner","type":"object","field_mappings":[{"name":"y","type":"i64"}]}]}`,
		`{"name":"c","type":"concatenate","concatenate_fields":["t"],"include_dynamic_fields":true,"tokenizer":"raw"}`,
	}
	var fields []json.RawMessage
	for _, in := range inputs {
		fields = append(fields, json.RawMessage(in))
	}
	tokenizers := []string{
		`{"name":"ng","type":"ngram","min_gram":2,"max_gram":4}`,
		`{"name":"re","type":"regex","pattern":"[a-z]+","filters":["lower_caser"]}`,
		`{"name":"simple_ascii","type":"simple","filters":["ascii_folding","remove_long"]}`,
	}
	var toks []json.RawMessage
	for _, in := range tokenizers {
		toks = append(toks, json.RawMessage(in))
	}

	id := fmt.Sprintf("roundtrip-%d", time.Now().UnixNano())
	_, err = c.CreateIndex(ctx, map[string]any{
		"version":  "0.9",
		"index_id": id,
		"doc_mapping": map[string]any{
			"field_mappings": fields, "timestamp_field": "ts", "tokenizers": toks,
			"dynamic_mapping": json.RawMessage(`{"stored":false}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DeleteIndex(ctx, id) })

	meta, err := c.GetIndex(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		DocMapping struct {
			FieldMappings  []json.RawMessage `json:"field_mappings"`
			Tokenizers     []json.RawMessage `json:"tokenizers"`
			DynamicMapping json.RawMessage   `json:"dynamic_mapping"`
		} `json:"doc_mapping"`
	}
	if err := json.Unmarshal(meta.IndexConfig, &cfg); err != nil {
		t.Fatal(err)
	}

	check := func(what, in, gotRaw string, canon func(string) (string, error)) {
		t.Helper()
		want, err := canon(in)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		got, err := mapping.SortedJSON(gotRaw)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s:\nserver: %s\n canon: %s", what, got, want)
		}
	}
	for i, in := range inputs {
		check("field "+strings.SplitN(in, `"`, 5)[3], in, string(cfg.DocMapping.FieldMappings[i]), mapping.Canonicalize)
	}
	for i, in := range tokenizers {
		check("tokenizer", in, string(cfg.DocMapping.Tokenizers[i]), mapping.CanonicalizeTokenizer)
	}
	check("dynamic_mapping", `{"stored":false}`, string(cfg.DocMapping.DynamicMapping), mapping.CanonicalizeDynamicMapping)

	// And the default dynamic mapping.
	id2 := id + "-dyn"
	if _, err := c.CreateIndex(ctx, map[string]any{"version": "0.9", "index_id": id2, "doc_mapping": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DeleteIndex(ctx, id2) })
	meta, err = c.GetIndex(ctx, id2)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(meta.IndexConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	check("default dynamic_mapping", `{"fast":{"normalizer":"raw"}}`, string(cfg.DocMapping.DynamicMapping), func(string) (string, error) {
		return mapping.DefaultDynamicMapping(), nil
	})
}
