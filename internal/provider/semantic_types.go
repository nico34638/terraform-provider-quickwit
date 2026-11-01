package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/nico34638/terraform-provider-quickwit/internal/mapping"
)

// A normalized string is a string whose value is compared through a
// normalization function: when the server returns a value that normalizes to
// the same thing as the one in state, the framework keeps the state value
// (semantic equality), so defaults, key order or unit spelling never show up
// as diffs.
//
// The normalizer is looked up by kind rather than stored as a func so that
// the type stays comparable.
type normKind string

const (
	kindFieldMapping   normKind = "field_mapping"
	kindTokenizer      normKind = "tokenizer"
	kindDynamicMapping normKind = "dynamic_mapping"
	kindByteSize       normKind = "byte_size"
	kindDuration       normKind = "duration"
	kindIndexURI       normKind = "index_uri"
)

var normalizers = map[normKind]func(string) (string, error){
	kindFieldMapping:   mapping.Canonicalize,
	kindTokenizer:      mapping.CanonicalizeTokenizer,
	kindDynamicMapping: mapping.CanonicalizeDynamicMapping,
	kindByteSize: func(s string) (string, error) {
		n, err := mapping.ParseByteSize(s)
		return strconv.FormatUint(n, 10), err
	},
	kindDuration: func(s string) (string, error) {
		d, err := mapping.ParseHumanDuration(s)
		return d.String(), err
	},
	kindIndexURI: mapping.NormalizeIndexURI,
}

var (
	fieldMappingType   = normalizedType{kind: kindFieldMapping}
	tokenizerType      = normalizedType{kind: kindTokenizer}
	dynamicMappingType = normalizedType{kind: kindDynamicMapping}
	byteSizeType       = normalizedType{kind: kindByteSize}
	durationType       = normalizedType{kind: kindDuration}
	indexURIType       = normalizedType{kind: kindIndexURI}
)

var (
	_ basetypes.StringTypable                    = normalizedType{}
	_ basetypes.StringValuableWithSemanticEquals = normalizedValue{}
	_ xattr.ValidateableAttribute                = normalizedValue{}
)

type normalizedType struct {
	basetypes.StringType
	kind normKind
}

func (t normalizedType) Equal(o attr.Type) bool {
	other, ok := o.(normalizedType)
	return ok && other.kind == t.kind
}

func (t normalizedType) String() string { return "normalizedType(" + string(t.kind) + ")" }

func (t normalizedType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return normalizedValue{StringValue: in, kind: t.kind}, nil
}

func (t normalizedType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	v, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	sv, ok := v.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", v)
	}
	out, diags := t.ValueFromString(ctx, sv)
	if diags.HasError() {
		return nil, fmt.Errorf("converting value: %v", diags)
	}
	return out, nil
}

func (t normalizedType) ValueType(context.Context) attr.Value { return normalizedValue{kind: t.kind} }

type normalizedValue struct {
	basetypes.StringValue
	kind normKind
}

func newNormalized(kind normKind, s string) normalizedValue {
	return normalizedValue{StringValue: basetypes.NewStringValue(s), kind: kind}
}

func newNormalizedNull(kind normKind) normalizedValue {
	return normalizedValue{StringValue: basetypes.NewStringNull(), kind: kind}
}

func (v normalizedValue) Type(context.Context) attr.Type { return normalizedType{kind: v.kind} }

func (v normalizedValue) Equal(o attr.Value) bool {
	other, ok := o.(normalizedValue)
	return ok && other.kind == v.kind && v.StringValue.Equal(other.StringValue)
}

func (v normalizedValue) normalized() (string, error) {
	return normalizers[v.kind](v.ValueString())
}

func (v normalizedValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	other, ok := newValuable.(normalizedValue)
	if !ok || other.kind != v.kind {
		return false, nil
	}
	a, err := v.normalized()
	if err != nil {
		return false, nil
	}
	b, err := other.normalized()
	if err != nil {
		return false, nil
	}
	return a == b, nil
}

// ValidateAttribute surfaces invalid JSON / units at plan time, with
// Quickwit's own error wording.
func (v normalizedValue) ValidateAttribute(_ context.Context, req xattr.ValidateAttributeRequest, resp *xattr.ValidateAttributeResponse) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	if _, err := v.normalized(); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", err.Error())
	}
}

// useStateWhenEquivalent plans the prior state value when the configured
// value is semantically equal to it. Semantic equality only applies to the
// values returned by Create, Read and Update: without this modifier, writing
// a value in another equivalent form ("2GB" -> "2000MB", reformatted JSON)
// would plan a no-op update, or a replacement for an attribute with
// RequiresReplace (which must come after it).
//
// Terraform core explicitly allows planning the prior value instead of a
// non-null configured one, for any attribute (assertPlannedValueValid in
// internal/plans/objchange/plan_valid.go: "configV and plannedV are
// functionally equivalent").
//
// On a list, the prior value is kept only when every element is equivalent,
// in order (Quickwit compares field mappings and tokenizers in order too).
type useStateWhenEquivalent struct{ kind normKind }

var (
	_ planmodifier.String = useStateWhenEquivalent{}
	_ planmodifier.List   = useStateWhenEquivalent{}
)

func (m useStateWhenEquivalent) Description(context.Context) string {
	return "Keeps the current value when the configured one is equivalent."
}

func (m useStateWhenEquivalent) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m useStateWhenEquivalent) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() || req.PlanValue.Equal(req.StateValue) {
		return
	}
	planned := normalizedValue{StringValue: req.PlanValue, kind: m.kind}
	equal, diags := planned.StringSemanticEquals(ctx, normalizedValue{StringValue: req.StateValue, kind: m.kind})
	resp.Diagnostics.Append(diags...)
	if equal {
		resp.PlanValue = req.StateValue
	}
}

func (m useStateWhenEquivalent) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() ||
		req.PlanValue.Equal(req.StateValue) {
		return
	}
	planned, prior := req.PlanValue.Elements(), req.StateValue.Elements()
	if len(planned) != len(prior) {
		return
	}
	for i := range planned {
		p, ok1 := planned[i].(normalizedValue)
		s, ok2 := prior[i].(normalizedValue)
		if !ok1 || !ok2 || p.IsNull() || p.IsUnknown() || s.IsNull() || s.IsUnknown() || p.kind != m.kind {
			return
		}
		equal, diags := p.StringSemanticEquals(ctx, s)
		resp.Diagnostics.Append(diags...)
		if !equal {
			return
		}
	}
	resp.PlanValue = req.StateValue
}
