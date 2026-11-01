package mapping

import (
	"strings"
	"testing"
	"time"
)

// Expected values are what Quickwit 0.9 returns from GET /indexes/{id} for
// the given input (captured from quickwit/quickwit:edge).
var canonicalCases = []struct {
	name, in, want string
}{
	{"datetime", `{"name":"ts","type":"datetime","fast":true}`,
		`{"fast":true,"fast_precision":"seconds","indexed":true,"input_formats":["rfc3339","unix_timestamp"],"name":"ts","output_format":"rfc3339","stored":true,"type":"datetime"}`},
	{"datetime precision alias", `{"name":"ts","type":"datetime","precision":"milliseconds"}`,
		`{"fast":false,"fast_precision":"milliseconds","indexed":true,"input_formats":["rfc3339","unix_timestamp"],"name":"ts","output_format":"rfc3339","stored":true,"type":"datetime"}`},
	{"text", `{"name":"t","type":"text"}`,
		`{"fast":false,"fieldnorms":false,"indexed":true,"name":"t","record":"basic","stored":true,"tokenizer":"default","type":"text"}`},
	{"text fast", `{"name":"t2","type":"text","fast":true,"tokenizer":"raw"}`,
		`{"fast":{"normalizer":"raw"},"fieldnorms":false,"indexed":true,"name":"t2","record":"basic","stored":true,"tokenizer":"raw","type":"text"}`},
	{"text not indexed", `{"name":"t3","type":"text","indexed":false}`,
		`{"fast":false,"fieldnorms":false,"indexed":false,"name":"t3","stored":true,"type":"text"}`},
	{"i64", `{"name":"n","type":"i64"}`,
		`{"coerce":true,"fast":false,"indexed":true,"name":"n","output_format":"number","stored":true,"type":"i64"}`},
	{"array u64", `{"name":"u","type":"array<u64>"}`,
		`{"coerce":true,"fast":false,"indexed":true,"name":"u","output_format":"number","stored":true,"type":"array<u64>"}`},
	{"bool", `{"name":"b","type":"bool","description":"flag"}`,
		`{"description":"flag","fast":false,"indexed":true,"name":"b","stored":true,"type":"bool"}`},
	{"ip", `{"name":"ip","type":"ip"}`,
		`{"fast":false,"indexed":true,"name":"ip","stored":true,"type":"ip"}`},
	{"bytes", `{"name":"by","type":"bytes"}`,
		`{"fast":false,"indexed":true,"input_format":"base64","name":"by","output_format":"base64","stored":true,"type":"bytes"}`},
	{"json", `{"name":"j","type":"json"}`,
		`{"expand_dots":true,"fast":false,"indexed":true,"name":"j","record":"basic","stored":true,"tokenizer":"raw","type":"json"}`},
	{"json not indexed", `{"name":"j2","type":"json","indexed":false}`,
		`{"expand_dots":true,"fast":false,"indexed":false,"name":"j2","stored":true,"type":"json"}`},
	{"object", `{"name":"o","type":"object","field_mappings":[{"name":"x","type":"text"}]}`,
		`{"field_mappings":[{"fast":false,"fieldnorms":false,"indexed":true,"name":"x","record":"basic","stored":true,"tokenizer":"default","type":"text"}],"name":"o","type":"object"}`},
	{"concatenate", `{"name":"c","type":"concatenate","concatenate_fields":["t"]}`,
		`{"concatenate_fields":["t"],"include_dynamic_fields":false,"name":"c","record":"basic","tokenizer":"default","type":"concatenate"}`},
}

func TestCanonicalize(t *testing.T) {
	for _, tc := range canonicalCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Canonicalize(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("\n got: %s\nwant: %s", got, tc.want)
			}
			again, err := Canonicalize(got)
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				t.Fatalf("not idempotent:\n%s\n%s", got, again)
			}
		})
	}
}

func TestCanonicalizeErrors(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"array bytes fast":    {`{"name":"b","type":"array<bytes>","fast":true}`, "fast field is not allowed for array<bytes>"},
		"empty concatenate":   {`{"name":"c","type":"concatenate"}`, "at least one sub-field"},
		"empty object":        {`{"name":"o","type":"object","field_mappings":[]}`, "at least one field mapping"},
		"unknown option":      {`{"name":"n","type":"i64","tokenizer":"raw"}`, `unknown field "tokenizer"`},
		"tokenizer unindexed": {`{"name":"t","type":"text","indexed":false,"tokenizer":"raw"}`, "allowed only if indexed"},
		"unknown type":        {`{"name":"x","type":"array<object>"}`, "unknown type"},
		"reserved name":       {`{"name":"_source","type":"text"}`, "reserved"},
		"bad name":            {`{"name":"1abc","type":"text"}`, "invalid"},
		"bad record":          {`{"name":"t","type":"text","record":"pos"}`, "invalid `record`"},
		"bad normalizer":      {`{"name":"t","type":"text","fast":{"normalizer":"upper"}}`, "unknown normalizer"},
		"nested error":        {`{"name":"o","type":"object","field_mappings":[{"name":"x","type":"nope"}]}`, "unknown type"},
		"duplicate child":     {`{"name":"o","type":"object","field_mappings":[{"name":"x","type":"text"},{"name":"x","type":"i64"}]}`, "duplicated"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Canonicalize(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestDynamicMapping(t *testing.T) {
	want := `{"expand_dots":true,"fast":{"normalizer":"raw"},"indexed":true,"record":"basic","stored":true,"tokenizer":"raw"}`
	if got := DefaultDynamicMapping(); got != want {
		t.Fatalf("\n got: %s\nwant: %s", got, want)
	}
}

func TestTokenizer(t *testing.T) {
	got, err := CanonicalizeTokenizer(`{"name":"ng","type":"ngram","min_gram":1,"max_gram":3}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"filters":[],"max_gram":3,"min_gram":1,"name":"ng","prefix_only":false,"type":"ngram"}`
	if got != want {
		t.Fatalf("\n got: %s\nwant: %s", got, want)
	}
	for _, bad := range []string{
		`{"name":"x","type":"ngram"}`,
		`{"name":"x","type":"regex"}`,
		`{"name":"x","type":"simple","pattern":"a"}`,
		`{"name":"x","type":"simple","filters":["upper"]}`,
		`{"name":"x","type":"what"}`,
	} {
		if _, err := CanonicalizeTokenizer(bad); err == nil {
			t.Errorf("expected error for %s", bad)
		}
	}
}

func TestLookup(t *testing.T) {
	var fields []*Field
	for _, raw := range []string{
		`{"name":"ts","type":"datetime","fast":true}`,
		`{"name":"resource","type":"object","field_mappings":[{"name":"service","type":"text","tokenizer":"raw"}]}`,
		`{"name":"a.b","type":"i64"}`,
	} {
		f, err := ParseField(raw)
		if err != nil {
			t.Fatal(err)
		}
		fields = append(fields, f)
	}
	for path, want := range map[string]string{"ts": "datetime", "resource.service": "text", `a\.b`: "i64", "a.b": "", "missing": "", "resource.x": ""} {
		f := Lookup(fields, path)
		got := ""
		if f != nil {
			got = f.Type
		}
		if got != want {
			t.Errorf("Lookup(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestUnits(t *testing.T) {
	for in, want := range map[string]uint64{"2GB": 2_000_000_000, "1 GiB": 1 << 30, "500mb": 500_000_000, "2000000000": 2_000_000_000, "1.5KB": 1500} {
		got, err := ParseByteSize(in)
		if err != nil || got != want {
			t.Errorf("ParseByteSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	// Beyond the uint64 range: an error, not a wrapped or saturated value.
	for _, in := range []string{"99999PB", "18446744073709551616", "1e30", "", "GB", "-1GB", "2XB"} {
		if got, err := ParseByteSize(in); err == nil {
			t.Errorf("ParseByteSize(%q) = %d, want an error", in, got)
		}
	}
	if got, err := ParseByteSize("16384PiB"); err == nil {
		t.Errorf("ParseByteSize(16384PiB) = %d, want an error (2^64)", got)
	}
	for in, want := range map[string]time.Duration{"2days": 48 * time.Hour, "1day 12h": 36 * time.Hour, "48h": 48 * time.Hour, "1h 30min": 90 * time.Minute, "7 days": 7 * 24 * time.Hour} {
		got, err := ParseHumanDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseHumanDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}
