// Package mapping mirrors the Quickwit doc mapping model
// (quickwit-doc-mapper/src/doc_mapper/field_mapping_entry.rs) closely enough to
// produce the exact JSON the server returns from GET /indexes/{id}.
//
// Canonical form = every default made explicit, keys sorted, options that the
// server drops (tokenizer/record of a non-indexed text field…) dropped. Two
// field mappings are equivalent for Quickwit iff their canonical forms are
// byte-identical.
package mapping

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Field types, as written in the `type` key (without the `array<…>` wrapper).
const (
	TypeText        = "text"
	TypeI64         = "i64"
	TypeU64         = "u64"
	TypeF64         = "f64"
	TypeBool        = "bool"
	TypeIP          = "ip"
	TypeDatetime    = "datetime"
	TypeBytes       = "bytes"
	TypeJSON        = "json"
	TypeObject      = "object"
	TypeConcatenate = "concatenate"
)

var (
	RecordOptions      = []string{"basic", "freq", "position"}
	Normalizers        = []string{"raw", "lowercase"}
	NumericOutputs     = []string{"number", "string"}
	BinaryFormats      = []string{"base64", "hex"}
	DatetimePrecisions = []string{"seconds", "milliseconds", "microseconds", "nanoseconds"}
)

// ReservedFieldNames are refused by Quickwit (QW_RESERVED_FIELD_NAMES).
var ReservedFieldNames = []string{"_doc_length", "_dynamic", "_field_presence", "_source"}

var fieldNamePattern = regexp.MustCompile(`^[@$_\-a-zA-Z][@$_/\.\-a-zA-Z0-9]{0,254}$`)

// ValidateFieldName applies validate_field_mapping_name.
func ValidateFieldName(name string) error {
	if slices.Contains(ReservedFieldNames, name) {
		return fmt.Errorf("field name `%s` is reserved for Quickwit internal usage (%s)", name, strings.Join(ReservedFieldNames, ", "))
	}
	if name == "" {
		return fmt.Errorf("field name is empty")
	}
	if !fieldNamePattern.MatchString(name) {
		return fmt.Errorf("field name `%s` is invalid: it must match %s", name, fieldNamePattern)
	}
	return nil
}

// ParseType splits "array<text>" into ("text", true).
func ParseType(typeID string) (base string, array bool, err error) {
	base = typeID
	if strings.HasPrefix(typeID, "array<") && strings.HasSuffix(typeID, ">") {
		base = strings.TrimSuffix(strings.TrimPrefix(typeID, "array<"), ">")
		array = true
	}
	switch base {
	case TypeText, TypeI64, TypeU64, TypeF64, TypeBool, TypeIP, TypeDatetime, TypeBytes, TypeJSON:
		return base, array, nil
	case TypeObject, TypeConcatenate:
		if array {
			return "", false, fmt.Errorf("unknown type `%s`", typeID)
		}
		return base, false, nil
	}
	return "", false, fmt.Errorf("unknown type `%s`", typeID)
}

// FastOption is the text/json `fast` option: `false`, `true` (raw normalizer)
// or `{"normalizer": "…"}`.
type FastOption struct {
	Enabled    bool
	Normalizer string
}

func (f *FastOption) UnmarshalJSON(b []byte) error {
	var enabled bool
	if err := json.Unmarshal(b, &enabled); err == nil {
		*f = FastOption{Enabled: enabled}
		if enabled {
			f.Normalizer = "raw"
		}
		return nil
	}
	var obj struct {
		Normalizer string `json:"normalizer"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return fmt.Errorf("`fast` must be a bool or {\"normalizer\": …}: %w", err)
	}
	if !slices.Contains(Normalizers, obj.Normalizer) {
		return fmt.Errorf("unknown normalizer `%s`, expected one of %v", obj.Normalizer, Normalizers)
	}
	*f = FastOption{Enabled: true, Normalizer: obj.Normalizer}
	return nil
}

func (f FastOption) MarshalJSON() ([]byte, error) {
	if !f.Enabled {
		return []byte("false"), nil
	}
	return json.Marshal(map[string]string{"normalizer": f.Normalizer})
}

// TextOptions mirrors QuickwitTextOptions.
type TextOptions struct {
	Description *string     `json:"description,omitempty"`
	Indexed     *bool       `json:"indexed,omitempty"`
	Tokenizer   *string     `json:"tokenizer,omitempty"`
	Record      *string     `json:"record,omitempty"`
	Fieldnorms  *bool       `json:"fieldnorms,omitempty"`
	Stored      *bool       `json:"stored,omitempty"`
	Fast        *FastOption `json:"fast,omitempty"`
}

func (o *TextOptions) canonicalize(bool) error {
	setDefault(&o.Indexed, true)
	setDefault(&o.Stored, true)
	setDefault(&o.Fast, FastOption{})
	if !*o.Indexed {
		if o.Tokenizer != nil || o.Record != nil || (o.Fieldnorms != nil && *o.Fieldnorms) {
			return fmt.Errorf("`record`, `tokenizer`, and `fieldnorms` parameters are allowed only if indexed is true")
		}
		setDefault(&o.Fieldnorms, false)
		return nil
	}
	setDefault(&o.Tokenizer, "default")
	setDefault(&o.Record, "basic")
	setDefault(&o.Fieldnorms, false)
	return checkEnum("record", *o.Record, RecordOptions)
}

// NumericOptions mirrors QuickwitNumericOptions (i64, u64, f64).
type NumericOptions struct {
	Description  *string `json:"description,omitempty"`
	Stored       *bool   `json:"stored,omitempty"`
	Indexed      *bool   `json:"indexed,omitempty"`
	Fast         *bool   `json:"fast,omitempty"`
	Coerce       *bool   `json:"coerce,omitempty"`
	OutputFormat *string `json:"output_format,omitempty"`
}

func (o *NumericOptions) canonicalize(bool) error {
	setDefault(&o.Stored, true)
	setDefault(&o.Indexed, true)
	setDefault(&o.Fast, false)
	setDefault(&o.Coerce, true)
	setDefault(&o.OutputFormat, "number")
	return checkEnum("output_format", *o.OutputFormat, NumericOutputs)
}

// BasicOptions mirrors QuickwitBoolOptions and QuickwitIpAddrOptions.
type BasicOptions struct {
	Description *string `json:"description,omitempty"`
	Stored      *bool   `json:"stored,omitempty"`
	Indexed     *bool   `json:"indexed,omitempty"`
	Fast        *bool   `json:"fast,omitempty"`
}

func (o *BasicOptions) canonicalize(bool) error {
	setDefault(&o.Stored, true)
	setDefault(&o.Indexed, true)
	setDefault(&o.Fast, false)
	return nil
}

// BytesOptions mirrors QuickwitBytesOptions.
type BytesOptions struct {
	Description  *string `json:"description,omitempty"`
	Stored       *bool   `json:"stored,omitempty"`
	Indexed      *bool   `json:"indexed,omitempty"`
	Fast         *bool   `json:"fast,omitempty"`
	InputFormat  *string `json:"input_format,omitempty"`
	OutputFormat *string `json:"output_format,omitempty"`
}

func (o *BytesOptions) canonicalize(array bool) error {
	setDefault(&o.Stored, true)
	setDefault(&o.Indexed, true)
	setDefault(&o.Fast, false)
	setDefault(&o.InputFormat, "base64")
	setDefault(&o.OutputFormat, "base64")
	if *o.Fast && array {
		return fmt.Errorf("fast field is not allowed for array<bytes>")
	}
	if err := checkEnum("input_format", *o.InputFormat, BinaryFormats); err != nil {
		return err
	}
	return checkEnum("output_format", *o.OutputFormat, BinaryFormats)
}

// DatetimeOptions mirrors QuickwitDateTimeOptions.
type DatetimeOptions struct {
	Description   *string  `json:"description,omitempty"`
	InputFormats  []string `json:"input_formats,omitempty"`
	OutputFormat  *string  `json:"output_format,omitempty"`
	FastPrecision *string  `json:"fast_precision,omitempty"`
	// Precision is the deprecated alias of fast_precision.
	Precision *string `json:"precision,omitempty"`
	Indexed   *bool   `json:"indexed,omitempty"`
	Stored    *bool   `json:"stored,omitempty"`
	Fast      *bool   `json:"fast,omitempty"`
}

func (o *DatetimeOptions) canonicalize(bool) error {
	if o.Precision != nil {
		if o.FastPrecision != nil {
			return fmt.Errorf("`precision` and `fast_precision` are aliases, set only one")
		}
		o.FastPrecision, o.Precision = o.Precision, nil
	}
	if o.InputFormats == nil {
		o.InputFormats = []string{"rfc3339", "unix_timestamp"}
	}
	if len(o.InputFormats) == 0 {
		return fmt.Errorf("`input_formats` must not be empty")
	}
	setDefault(&o.OutputFormat, "rfc3339")
	setDefault(&o.FastPrecision, "seconds")
	setDefault(&o.Indexed, true)
	setDefault(&o.Stored, true)
	setDefault(&o.Fast, false)
	return checkEnum("fast_precision", *o.FastPrecision, DatetimePrecisions)
}

// JSONOptions mirrors QuickwitJsonOptions (also used for dynamic_mapping).
type JSONOptions struct {
	Description *string     `json:"description,omitempty"`
	Indexed     *bool       `json:"indexed,omitempty"`
	Tokenizer   *string     `json:"tokenizer,omitempty"`
	Record      *string     `json:"record,omitempty"`
	Stored      *bool       `json:"stored,omitempty"`
	ExpandDots  *bool       `json:"expand_dots,omitempty"`
	Fast        *FastOption `json:"fast,omitempty"`
}

func (o *JSONOptions) canonicalize(bool) error {
	setDefault(&o.Indexed, true)
	setDefault(&o.Stored, true)
	setDefault(&o.ExpandDots, true)
	setDefault(&o.Fast, FastOption{})
	if !*o.Indexed {
		if o.Tokenizer != nil || o.Record != nil {
			return fmt.Errorf("`record` and `tokenizer` parameters are allowed only if indexed is true")
		}
		return nil
	}
	setDefault(&o.Tokenizer, "raw")
	setDefault(&o.Record, "basic")
	return checkEnum("record", *o.Record, RecordOptions)
}

// ConcatenateOptions mirrors QuickwitConcatenateOptions.
type ConcatenateOptions struct {
	Description          *string  `json:"description,omitempty"`
	ConcatenateFields    []string `json:"concatenate_fields"`
	IncludeDynamicFields *bool    `json:"include_dynamic_fields,omitempty"`
	Tokenizer            *string  `json:"tokenizer,omitempty"`
	Record               *string  `json:"record,omitempty"`
}

func (o *ConcatenateOptions) canonicalize(bool) error {
	if o.ConcatenateFields == nil {
		o.ConcatenateFields = []string{}
	}
	setDefault(&o.IncludeDynamicFields, false)
	setDefault(&o.Tokenizer, "default")
	setDefault(&o.Record, "basic")
	if len(o.ConcatenateFields) == 0 && !*o.IncludeDynamicFields {
		return fmt.Errorf("concatenate type must have at least one sub-field")
	}
	return checkEnum("record", *o.Record, RecordOptions)
}

// ObjectOptions mirrors QuickwitObjectOptions.
type ObjectOptions struct {
	FieldMappings []json.RawMessage `json:"field_mappings"`
}

func (o *ObjectOptions) canonicalize(bool) error {
	if len(o.FieldMappings) == 0 {
		return fmt.Errorf("object type must have at least one field mapping")
	}
	seen := map[string]bool{}
	for i, raw := range o.FieldMappings {
		canon, name, err := canonicalizeField(raw)
		if err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("duplicated field definition `%s`", name)
		}
		seen[name] = true
		o.FieldMappings[i] = canon
	}
	return nil
}

type options interface {
	canonicalize(array bool) error
}

func newOptions(base string) options {
	switch base {
	case TypeText:
		return &TextOptions{}
	case TypeI64, TypeU64, TypeF64:
		return &NumericOptions{}
	case TypeBool, TypeIP:
		return &BasicOptions{}
	case TypeBytes:
		return &BytesOptions{}
	case TypeDatetime:
		return &DatetimeOptions{}
	case TypeJSON:
		return &JSONOptions{}
	case TypeConcatenate:
		return &ConcatenateOptions{}
	case TypeObject:
		return &ObjectOptions{}
	}
	panic("unreachable: " + base)
}

// Field is the parsed form of one field mapping entry: name, type and the
// canonical options.
type Field struct {
	Name    string
	Type    string // base type, e.g. "text"
	Array   bool
	Options map[string]any
}

// Canonicalize applies Quickwit's defaults to a field mapping JSON document
// and returns its canonical encoding.
func Canonicalize(raw string) (string, error) {
	canon, _, err := canonicalizeField(json.RawMessage(raw))
	if err != nil {
		return "", err
	}
	return string(canon), nil
}

// ParseField canonicalizes a field mapping and returns its parsed form.
func ParseField(raw string) (*Field, error) {
	canon, _, err := canonicalizeField(json.RawMessage(raw))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(canon, &m); err != nil {
		return nil, err
	}
	base, array, _ := ParseType(m["type"].(string))
	f := &Field{Name: m["name"].(string), Type: base, Array: array, Options: m}
	return f, nil
}

func canonicalizeField(raw json.RawMessage) (json.RawMessage, string, error) {
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, "", fmt.Errorf("field mapping is not a JSON object: %w", err)
	}
	var name, typeID string
	if err := unmarshalKey(entry, "name", &name); err != nil {
		return nil, "", err
	}
	if err := ValidateFieldName(name); err != nil {
		return nil, "", err
	}
	if err := unmarshalKey(entry, "type", &typeID); err != nil {
		return nil, "", fmt.Errorf("field `%s`: %w", name, err)
	}
	delete(entry, "name")
	delete(entry, "type")
	base, array, err := ParseType(typeID)
	if err != nil {
		return nil, "", fmt.Errorf("field `%s` has an %w", name, err)
	}

	opts := newOptions(base)
	rest, _ := json.Marshal(entry)
	if err := decodeStrict(rest, opts); err != nil {
		return nil, "", fmt.Errorf("error while parsing field `%s`: %w", name, err)
	}
	if err := opts.canonicalize(array); err != nil {
		return nil, "", fmt.Errorf("error while parsing field `%s`: %w", name, err)
	}

	out, err := sortedJSON(opts)
	if err != nil {
		return nil, "", err
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	m["name"] = name
	m["type"] = typeID
	canon, err := sortedJSON(m)
	return canon, name, err
}

// CanonicalizeDynamicMapping canonicalizes a `dynamic_mapping` JSON object.
// Its options default like those of a json field (`fast` included: the raw
// fast normalizer only applies when dynamic_mapping is omitted altogether,
// see DefaultDynamicMapping).
func CanonicalizeDynamicMapping(raw string) (string, error) {
	var o JSONOptions
	if err := decodeStrict([]byte(raw), &o); err != nil {
		return "", fmt.Errorf("invalid dynamic_mapping: %w", err)
	}
	if err := o.canonicalize(false); err != nil {
		return "", fmt.Errorf("invalid dynamic_mapping: %w", err)
	}
	out, err := sortedJSON(&o)
	return string(out), err
}

// DefaultDynamicMapping is the canonical dynamic_mapping Quickwit applies
// when the mode is dynamic and none is given (QuickwitJsonOptions::default_dynamic).
func DefaultDynamicMapping() string {
	s, _ := CanonicalizeDynamicMapping(`{"fast":{"normalizer":"raw"}}`)
	return s
}

// Lookup resolves a dotted path (e.g. "resource.service") through object
// fields. Escaped dots (`\.`) are part of a field name.
func Lookup(fields []*Field, path string) *Field {
	head, tail, nested := splitPath(path)
	if !nested {
		for _, f := range fields {
			if f.Name == head {
				return f
			}
		}
		return nil
	}
	for _, f := range fields {
		if f.Name != head || f.Type != TypeObject {
			continue
		}
		var children []*Field
		for _, raw := range f.Options["field_mappings"].([]any) {
			b, _ := json.Marshal(raw)
			c, err := ParseField(string(b))
			if err != nil {
				return nil
			}
			children = append(children, c)
		}
		return Lookup(children, tail)
	}
	return nil
}

func splitPath(path string) (head, tail string, ok bool) {
	for i := 0; i < len(path); i++ {
		if path[i] == '\\' {
			i++
			continue
		}
		if path[i] == '.' {
			return strings.ReplaceAll(path[:i], `\.`, "."), path[i+1:], true
		}
	}
	return strings.ReplaceAll(path, `\.`, "."), "", false
}

func setDefault[T any](p **T, v T) {
	if *p == nil {
		*p = &v
	}
}

func checkEnum(key, v string, allowed []string) error {
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("invalid `%s` value `%s`, expected one of %v", key, v, allowed)
	}
	return nil
}

func unmarshalKey(entry map[string]json.RawMessage, key string, dst *string) error {
	v, ok := entry[key]
	if !ok {
		return fmt.Errorf("missing `%s`", key)
	}
	if err := json.Unmarshal(v, dst); err != nil {
		return fmt.Errorf("`%s` must be a string", key)
	}
	return nil
}

// decodeStrict mimics serde's deny_unknown_fields.
func decodeStrict(b []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// sortedJSON marshals v with every object's keys sorted, at any depth.
func sortedJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return marshal(generic) // encoding/json sorts map keys
}

// marshal is json.Marshal without HTML escaping, so "array<text>" stays
// readable.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// SortedJSON re-encodes a JSON document with sorted keys and no whitespace.
func SortedJSON(raw string) (string, error) {
	var generic any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return "", err
	}
	b, err := marshal(generic)
	return string(b), err
}
