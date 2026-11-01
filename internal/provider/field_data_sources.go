package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nico34638/terraform-provider-quickwit/internal/mapping"
)

// Field data sources work like aws_iam_policy_document: typed, validated
// inputs in, one canonical JSON document out (attribute `json`), meant to be
// passed to quickwit_index.doc_mapping.field_mappings.
//
// They are declared from a spec: each attribute is copied under the same JSON
// key, except a few synthetic ones (`array`, `fast_normalizer`, numeric
// `type`) handled in buildFieldJSON.

type attrKind int

const (
	kString attrKind = iota
	kBool
	kInt
	kStringList
)

type dsAttr struct {
	name       string
	kind       attrKind
	required   bool
	desc       string
	validators []validator.String
}

type dsSpec struct {
	typeName string // data source suffix, e.g. "field_text"
	desc     string
	attrs    []dsAttr
	// build turns the set attributes into the raw JSON document and returns
	// its canonical form.
	build func(vals map[string]any) (string, error)
}

func oneOf(values ...string) []validator.String {
	return []validator.String{stringvalidator.OneOf(values...)}
}

// Attribute building blocks.
var (
	aName = dsAttr{name: "name", kind: kString, required: true, desc: "Field name. Dots must be escaped (`\\\\.`) to be part of the name."}
	aDesc = dsAttr{name: "description", kind: kString, desc: "Free-form description of the field."}
	aArr  = dsAttr{name: "array", kind: kBool, desc: "Map an array of values (`array<T>`). Defaults to `false`."}
	aStor = dsAttr{name: "stored", kind: kBool, desc: "Store the value in the doc store. Defaults to `true`."}
	aIdx  = dsAttr{name: "indexed", kind: kBool, desc: "Index the field (make it searchable). Defaults to `true`."}
	aFast = dsAttr{name: "fast", kind: kBool, desc: "Store the field in a columnar fast field (sorting, aggregations). Defaults to `false`."}
	aNorm = dsAttr{name: "fast_normalizer", kind: kString, validators: oneOf(mapping.Normalizers...),
		desc: "Normalizer of the fast field (`raw` or `lowercase`). Implies `fast = true`; defaults to `raw` when `fast = true`."}
	aRec = dsAttr{name: "record", kind: kString, validators: oneOf(mapping.RecordOptions...),
		desc: "What the inverted index records: `basic` (doc ids), `freq` (+ term frequencies) or `position` (+ positions, needed for phrase queries). Defaults to `basic`."}
)

func tokenizerAttr(def string) dsAttr {
	return dsAttr{name: "tokenizer", kind: kString, desc: fmt.Sprintf("Tokenizer name: a built-in one (`raw`, `default`, `en_stem`, `whitespace`, `chinese_compatible`, `source_code_default`, `lowercase`…) or a custom `quickwit_tokenizer`. Defaults to `%s`.", def)}
}

func fieldSpecs() []dsSpec {
	return []dsSpec{
		{
			typeName: "field_text", desc: "Builds a `text` field mapping.",
			attrs: []dsAttr{aName, aDesc, aArr, aStor, aIdx, aFast, aNorm, tokenizerAttr("default"), aRec,
				{name: "fieldnorms", kind: kBool, desc: "Store field norms (needed for BM25 length normalization). Defaults to `false`."}},
			build: simpleField(mapping.TypeText),
		},
		{
			typeName: "field_numeric", desc: "Builds an `i64`, `u64` or `f64` field mapping.",
			attrs: []dsAttr{aName,
				{name: "type", kind: kString, required: true, validators: oneOf(mapping.TypeI64, mapping.TypeU64, mapping.TypeF64), desc: "Numeric type: `i64`, `u64` or `f64`."},
				aDesc, aArr, aStor, aIdx, aFast,
				{name: "coerce", kind: kBool, desc: "Accept numbers sent as JSON strings. Defaults to `true`."},
				{name: "output_format", kind: kString, validators: oneOf(mapping.NumericOutputs...), desc: "Output format in search results: `number` or `string`. Defaults to `number`."},
			},
			build: func(v map[string]any) (string, error) {
				t := v["type"].(string)
				delete(v, "type")
				return simpleField(t)(v)
			},
		},
		{typeName: "field_bool", desc: "Builds a `bool` field mapping.", attrs: []dsAttr{aName, aDesc, aArr, aStor, aIdx, aFast}, build: simpleField(mapping.TypeBool)},
		{typeName: "field_ip", desc: "Builds an `ip` field mapping (IPv4 and IPv6).", attrs: []dsAttr{aName, aDesc, aArr, aStor, aIdx, aFast}, build: simpleField(mapping.TypeIP)},
		{
			typeName: "field_datetime", desc: "Builds a `datetime` field mapping. The index `timestamp_field` must be a single-valued fast datetime field.",
			attrs: []dsAttr{aName, aDesc, aArr, aStor, aIdx, aFast,
				{name: "input_formats", kind: kStringList, desc: "Accepted input formats: `rfc3339`, `rfc2822`, `iso8601`, `unix_timestamp` or a strptime pattern. Defaults to `[\"rfc3339\", \"unix_timestamp\"]`."},
				{name: "output_format", kind: kString, desc: "Output format in search results: `rfc3339`, `rfc2822`, `iso8601`, `unix_timestamp_secs|millis|micros|nanos` or a strptime pattern. Defaults to `rfc3339`."},
				{name: "fast_precision", kind: kString, validators: oneOf(mapping.DatetimePrecisions...), desc: "Precision of the fast field. Defaults to `seconds`."},
			},
			build: simpleField(mapping.TypeDatetime),
		},
		{
			typeName: "field_bytes", desc: "Builds a `bytes` field mapping.",
			attrs: []dsAttr{aName, aDesc, aArr, aStor, aIdx,
				{name: "fast", kind: kBool, desc: "Store the field in a fast field. Not allowed with `array = true`. Defaults to `false`."},
				{name: "input_format", kind: kString, validators: oneOf(mapping.BinaryFormats...), desc: "Input encoding: `base64` or `hex`. Defaults to `base64`."},
				{name: "output_format", kind: kString, validators: oneOf(mapping.BinaryFormats...), desc: "Output encoding: `base64` or `hex`. Defaults to `base64`."},
			},
			build: simpleField(mapping.TypeBytes),
		},
		{
			typeName: "field_json", desc: "Builds a `json` field mapping (schemaless sub-document).",
			attrs: []dsAttr{aName, aDesc, aArr, aStor, aIdx, aFast, aNorm, tokenizerAttr("raw"), aRec,
				{name: "expand_dots", kind: kBool, desc: "Treat dotted keys (`{\"a.b\": 1}`) as nested objects. Defaults to `true`."}},
			build: simpleField(mapping.TypeJSON),
		},
		{
			typeName: "field_object", desc: "Builds an `object` field mapping from child field mappings (themselves outputs of `quickwit_field_*` data sources).",
			attrs: []dsAttr{aName,
				{name: "field_mappings", kind: kStringList, required: true, desc: "Child field mappings, as JSON documents (`data.quickwit_field_*.x.json`). Must not be empty."}},
			build: func(v map[string]any) (string, error) {
				children := []json.RawMessage{}
				for _, s := range v["field_mappings"].([]string) {
					children = append(children, json.RawMessage(s))
				}
				v["field_mappings"] = children
				return simpleField(mapping.TypeObject)(v)
			},
		},
		{
			typeName: "field_concatenate", desc: "Builds a `concatenate` field mapping: a virtual field indexing several fields together.",
			attrs: []dsAttr{aName, aDesc,
				{name: "concatenate_fields", kind: kStringList, desc: "Fields to concatenate."},
				{name: "include_dynamic_fields", kind: kBool, desc: "Also concatenate dynamic (unmapped) fields. Defaults to `false`."},
				tokenizerAttr("default"), aRec},
			build: simpleField(mapping.TypeConcatenate),
		},
		{
			typeName: "tokenizer", desc: "Builds a custom tokenizer, to pass to `quickwit_index.doc_mapping.tokenizers`.",
			attrs: []dsAttr{aName,
				{name: "type", kind: kString, required: true, validators: oneOf(mapping.TokenizerTypes...), desc: "Tokenizer type: `ngram`, `regex`, `simple` or `source_code`."},
				{name: "min_gram", kind: kInt, desc: "ngram: minimum gram length."},
				{name: "max_gram", kind: kInt, desc: "ngram: maximum gram length."},
				{name: "prefix_only", kind: kBool, desc: "ngram: only emit grams starting at the beginning of the token. Defaults to `false`."},
				{name: "pattern", kind: kString, desc: "regex: the token pattern."},
				{name: "filters", kind: kStringList, desc: "Token filters, applied in order: `remove_long`, `lower_caser`, `ascii_folding`."},
			},
			build: func(v map[string]any) (string, error) {
				b, err := json.Marshal(v)
				if err != nil {
					return "", err
				}
				return mapping.CanonicalizeTokenizer(string(b))
			},
		},
	}
}

// simpleField copies attributes to JSON keys and resolves the synthetic ones.
func simpleField(baseType string) func(map[string]any) (string, error) {
	return func(v map[string]any) (string, error) {
		typeID := baseType
		if arr, _ := v["array"].(bool); arr {
			typeID = "array<" + baseType + ">"
		}
		delete(v, "array")
		if norm, ok := v["fast_normalizer"].(string); ok {
			if fast, set := v["fast"].(bool); set && !fast {
				return "", fmt.Errorf("`fast_normalizer` requires `fast` to be true (or unset)")
			}
			v["fast"] = map[string]string{"normalizer": norm}
			delete(v, "fast_normalizer")
		}
		v["type"] = typeID
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return mapping.Canonicalize(string(b))
	}
}

func fieldDataSources() []func() datasource.DataSource {
	var out []func() datasource.DataSource
	for _, spec := range fieldSpecs() {
		out = append(out, func() datasource.DataSource { return &fieldDataSource{spec: spec} })
	}
	return out
}

var (
	_ datasource.DataSource                   = (*fieldDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*fieldDataSource)(nil)
)

type fieldDataSource struct {
	spec dsSpec
}

func (d *fieldDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.spec.typeName
}

func (d *fieldDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"json": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Canonical JSON document (all defaults explicit, keys sorted), as returned by Quickwit.",
		},
	}
	for _, a := range d.spec.attrs {
		optional := !a.required
		switch a.kind {
		case kString:
			attrs[a.name] = schema.StringAttribute{Required: a.required, Optional: optional, MarkdownDescription: a.desc, Validators: a.validators}
		case kBool:
			attrs[a.name] = schema.BoolAttribute{Required: a.required, Optional: optional, MarkdownDescription: a.desc}
		case kInt:
			attrs[a.name] = schema.Int64Attribute{Required: a.required, Optional: optional, MarkdownDescription: a.desc}
		case kStringList:
			attrs[a.name] = schema.ListAttribute{Required: a.required, Optional: optional, ElementType: types.StringType, MarkdownDescription: a.desc}
		}
	}
	resp.Schema = schema.Schema{MarkdownDescription: d.spec.desc, Attributes: attrs}
}

// values reads the set attributes of the config. known is false when any
// attribute is still unknown (computed from another resource).
func (d *fieldDataSource) values(ctx context.Context, cfg tfsdk.Config) (vals map[string]any, known bool, diags diag.Diagnostics) {
	vals = map[string]any{}
	for _, a := range d.spec.attrs {
		p := path.Root(a.name)
		switch a.kind {
		case kString:
			var v types.String
			diags.Append(cfg.GetAttribute(ctx, p, &v)...)
			if v.IsUnknown() {
				return nil, false, diags
			}
			if !v.IsNull() {
				vals[a.name] = v.ValueString()
			}
		case kBool:
			var v types.Bool
			diags.Append(cfg.GetAttribute(ctx, p, &v)...)
			if v.IsUnknown() {
				return nil, false, diags
			}
			if !v.IsNull() {
				vals[a.name] = v.ValueBool()
			}
		case kInt:
			var v types.Int64
			diags.Append(cfg.GetAttribute(ctx, p, &v)...)
			if v.IsUnknown() {
				return nil, false, diags
			}
			if !v.IsNull() {
				vals[a.name] = v.ValueInt64()
			}
		case kStringList:
			var v types.List
			diags.Append(cfg.GetAttribute(ctx, p, &v)...)
			if v.IsUnknown() {
				return nil, false, diags
			}
			if !v.IsNull() {
				var elems []types.String
				diags.Append(v.ElementsAs(ctx, &elems, false)...)
				list := make([]string, 0, len(elems))
				for _, e := range elems {
					if e.IsUnknown() {
						return nil, false, diags
					}
					list = append(list, e.ValueString())
				}
				vals[a.name] = list
			}
		}
	}
	return vals, true, diags
}

func (d *fieldDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	vals, known, diags := d.values(ctx, req.Config)
	resp.Diagnostics.Append(diags...)
	if !known || resp.Diagnostics.HasError() {
		return
	}
	if _, err := d.spec.build(vals); err != nil {
		resp.Diagnostics.AddError("Invalid "+d.spec.typeName, err.Error())
	}
}

func (d *fieldDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	vals, _, diags := d.values(ctx, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := d.spec.build(vals)
	if err != nil {
		resp.Diagnostics.AddError("Invalid "+d.spec.typeName, err.Error())
		return
	}
	resp.State.Raw = req.Config.Raw
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("json"), types.StringValue(out))...)
}
