package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
}

var (
	fieldMappingType   = normalizedType{kind: kindFieldMapping}
	tokenizerType      = normalizedType{kind: kindTokenizer}
	dynamicMappingType = normalizedType{kind: kindDynamicMapping}
	byteSizeType       = normalizedType{kind: kindByteSize}
	durationType       = normalizedType{kind: kindDuration}
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
