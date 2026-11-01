package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/nico34638/terraform-provider-quickwit/internal/client"
	"github.com/nico34638/terraform-provider-quickwit/internal/mapping"
)

// IndexModel is exported so that the data source model can embed it.
type IndexModel struct {
	ID               types.String `tfsdk:"id"`
	IndexID          types.String `tfsdk:"index_id"`
	IndexURI         types.String `tfsdk:"index_uri"`
	IndexUID         types.String `tfsdk:"index_uid"`
	DocMapping       types.Object `tfsdk:"doc_mapping"`
	IndexingSettings types.Object `tfsdk:"indexing_settings"`
	IngestSettings   types.Object `tfsdk:"ingest_settings"`
	SearchSettings   types.Object `tfsdk:"search_settings"`
	Retention        types.Object `tfsdk:"retention"`
}

type docMappingModel struct {
	Mode               types.String    `tfsdk:"mode"`
	DynamicMapping     normalizedValue `tfsdk:"dynamic_mapping"`
	FieldMappings      types.List      `tfsdk:"field_mappings"`
	TimestampField     types.String    `tfsdk:"timestamp_field"`
	TagFields          types.Set       `tfsdk:"tag_fields"`
	PartitionKey       types.String    `tfsdk:"partition_key"`
	MaxNumPartitions   types.Int64     `tfsdk:"max_num_partitions"`
	IndexFieldPresence types.Bool      `tfsdk:"index_field_presence"`
	StoreDocumentSize  types.Bool      `tfsdk:"store_document_size"`
	StoreSource        types.Bool      `tfsdk:"store_source"`
	Tokenizers         types.List      `tfsdk:"tokenizers"`
	DocMappingUID      types.String    `tfsdk:"doc_mapping_uid"`
}

type indexingSettingsModel struct {
	CommitTimeoutSecs        types.Int64     `tfsdk:"commit_timeout_secs"`
	DocstoreCompressionLevel types.Int64     `tfsdk:"docstore_compression_level"`
	DocstoreBlocksize        types.Int64     `tfsdk:"docstore_blocksize"`
	SplitNumDocsTarget       types.Int64     `tfsdk:"split_num_docs_target"`
	HeapSize                 normalizedValue `tfsdk:"heap_size"`
	MergePolicy              types.Object    `tfsdk:"merge_policy"`
}

type mergePolicyModel struct {
	Type                       types.String    `tfsdk:"type"`
	MinLevelNumDocs            types.Int64     `tfsdk:"min_level_num_docs"`
	MergeFactor                types.Int64     `tfsdk:"merge_factor"`
	MaxMergeFactor             types.Int64     `tfsdk:"max_merge_factor"`
	MaxMergeOps                types.Int64     `tfsdk:"max_merge_ops"`
	MaturationPeriod           normalizedValue `tfsdk:"maturation_period"`
	MaxFinalizeMergeOperations types.Int64     `tfsdk:"max_finalize_merge_operations"`
	MaxFinalizeSplitNumDocs    types.Int64     `tfsdk:"max_finalize_split_num_docs"`
}

func (m *mergePolicyModel) ints() map[string]*types.Int64 {
	return map[string]*types.Int64{
		"min_level_num_docs":            &m.MinLevelNumDocs,
		"merge_factor":                  &m.MergeFactor,
		"max_merge_factor":              &m.MaxMergeFactor,
		"max_merge_ops":                 &m.MaxMergeOps,
		"max_finalize_merge_operations": &m.MaxFinalizeMergeOperations,
		"max_finalize_split_num_docs":   &m.MaxFinalizeSplitNumDocs,
	}
}

type ingestSettingsModel struct {
	MinShards    types.Int64 `tfsdk:"min_shards"`
	ValidateDocs types.Bool  `tfsdk:"validate_docs"`
}

type searchSettingsModel struct {
	DefaultSearchFields types.List `tfsdk:"default_search_fields"`
}

type retentionModel struct {
	Period                   types.String    `tfsdk:"period"`
	Schedule                 types.String    `tfsdk:"schedule"`
	EvaluationScheduleJitter normalizedValue `tfsdk:"evaluation_schedule_jitter"`
}

// Object attribute types, derived from the schema so they cannot drift.
type objectTypes struct {
	docMapping, indexing, mergePolicy, ingest, search, retention map[string]attr.Type
}

func newObjectTypes() objectTypes {
	s := indexSchema()
	nested := func(a schema.Attribute) map[string]attr.Type {
		return a.GetType().(types.ObjectType).AttrTypes
	}
	indexing := nested(s.Attributes["indexing_settings"])
	return objectTypes{
		docMapping:  nested(s.Attributes["doc_mapping"]),
		indexing:    indexing,
		mergePolicy: indexing["merge_policy"].(types.ObjectType).AttrTypes,
		ingest:      nested(s.Attributes["ingest_settings"]),
		search:      nested(s.Attributes["search_settings"]),
		retention:   nested(s.Attributes["retention"]),
	}
}

var objTypes = newObjectTypes()

// ---------------------------------------------------------------------------
// API representation
// ---------------------------------------------------------------------------

type apiIndexConfig struct {
	Version          string              `json:"version"`
	IndexID          string              `json:"index_id"`
	IndexURI         string              `json:"index_uri,omitempty"`
	DocMapping       apiDocMapping       `json:"doc_mapping"`
	IndexingSettings apiIndexingSettings `json:"indexing_settings"`
	IngestSettings   apiIngestSettings   `json:"ingest_settings"`
	SearchSettings   apiSearchSettings   `json:"search_settings"`
	Retention        *apiRetention       `json:"retention,omitempty"`
}

type apiDocMapping struct {
	DocMappingUID      string            `json:"doc_mapping_uid,omitempty"`
	Mode               string            `json:"mode"`
	DynamicMapping     json.RawMessage   `json:"dynamic_mapping,omitempty"`
	FieldMappings      []json.RawMessage `json:"field_mappings"`
	TimestampField     *string           `json:"timestamp_field,omitempty"`
	TagFields          []string          `json:"tag_fields"`
	PartitionKey       *string           `json:"partition_key,omitempty"`
	MaxNumPartitions   int64             `json:"max_num_partitions"`
	IndexFieldPresence bool              `json:"index_field_presence"`
	StoreDocumentSize  bool              `json:"store_document_size"`
	StoreSource        bool              `json:"store_source"`
	Tokenizers         []json.RawMessage `json:"tokenizers"`
}

type apiIndexingSettings struct {
	CommitTimeoutSecs        int64          `json:"commit_timeout_secs"`
	DocstoreCompressionLevel int64          `json:"docstore_compression_level"`
	DocstoreBlocksize        int64          `json:"docstore_blocksize"`
	SplitNumDocsTarget       int64          `json:"split_num_docs_target"`
	MergePolicy              map[string]any `json:"merge_policy"`
	Resources                struct {
		HeapSize json.RawMessage `json:"heap_size"` // number of bytes on read, "2GB" on write
	} `json:"resources"`
}

type apiIngestSettings struct {
	MinShards    int64 `json:"min_shards"`
	ValidateDocs *bool `json:"validate_docs,omitempty"`
}

type apiSearchSettings struct {
	DefaultSearchFields []string `json:"default_search_fields"`
}

type apiRetention struct {
	Period                   string  `json:"period"`
	Schedule                 string  `json:"schedule,omitempty"`
	EvaluationScheduleJitter *string `json:"evaluation_schedule_jitter,omitempty"`
}

// ---------------------------------------------------------------------------
// plan → API
// ---------------------------------------------------------------------------

// toAPI builds the full index config from a plan. doc_mapping_uid is never
// sent: Quickwit keeps the current one when the mapping is unchanged and
// generates a new one otherwise.
func toAPI(ctx context.Context, m IndexModel) (*apiIndexConfig, diag.Diagnostics) {
	var diags diag.Diagnostics
	cfg := &apiIndexConfig{Version: "0.9", IndexID: m.IndexID.ValueString()}
	if !m.IndexURI.IsNull() && !m.IndexURI.IsUnknown() {
		cfg.IndexURI = m.IndexURI.ValueString()
	}

	var dm docMappingModel
	diags.Append(m.DocMapping.As(ctx, &dm, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, diags
	}
	cfg.DocMapping = apiDocMapping{
		Mode:               dm.Mode.ValueString(),
		FieldMappings:      rawList(ctx, dm.FieldMappings, &diags),
		TimestampField:     dm.TimestampField.ValueStringPointer(),
		PartitionKey:       dm.PartitionKey.ValueStringPointer(),
		MaxNumPartitions:   dm.MaxNumPartitions.ValueInt64(),
		IndexFieldPresence: dm.IndexFieldPresence.ValueBool(),
		StoreDocumentSize:  dm.StoreDocumentSize.ValueBool(),
		StoreSource:        dm.StoreSource.ValueBool(),
		Tokenizers:         rawList(ctx, dm.Tokenizers, &diags),
		TagFields:          []string{},
	}
	if !dm.DynamicMapping.IsNull() {
		cfg.DocMapping.DynamicMapping = json.RawMessage(dm.DynamicMapping.ValueString())
	}
	diags.Append(dm.TagFields.ElementsAs(ctx, &cfg.DocMapping.TagFields, false)...)

	var is indexingSettingsModel
	diags.Append(m.IndexingSettings.As(ctx, &is, basetypes.ObjectAsOptions{})...)
	var mp mergePolicyModel
	diags.Append(is.MergePolicy.As(ctx, &mp, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, diags
	}
	cfg.IndexingSettings = apiIndexingSettings{
		CommitTimeoutSecs:        is.CommitTimeoutSecs.ValueInt64(),
		DocstoreCompressionLevel: is.DocstoreCompressionLevel.ValueInt64(),
		DocstoreBlocksize:        is.DocstoreBlocksize.ValueInt64(),
		SplitNumDocsTarget:       is.SplitNumDocsTarget.ValueInt64(),
		MergePolicy:              map[string]any{"type": mp.Type.ValueString()},
	}
	cfg.IndexingSettings.Resources.HeapSize, _ = json.Marshal(is.HeapSize.ValueString())
	for k, v := range mp.ints() {
		if !v.IsNull() {
			cfg.IndexingSettings.MergePolicy[k] = v.ValueInt64()
		}
	}
	if !mp.MaturationPeriod.IsNull() {
		cfg.IndexingSettings.MergePolicy["maturation_period"] = mp.MaturationPeriod.ValueString()
	}

	var ing ingestSettingsModel
	diags.Append(m.IngestSettings.As(ctx, &ing, basetypes.ObjectAsOptions{})...)
	cfg.IngestSettings = apiIngestSettings{MinShards: ing.MinShards.ValueInt64(), ValidateDocs: ing.ValidateDocs.ValueBoolPointer()}

	var ss searchSettingsModel
	diags.Append(m.SearchSettings.As(ctx, &ss, basetypes.ObjectAsOptions{})...)
	cfg.SearchSettings.DefaultSearchFields = []string{}
	diags.Append(ss.DefaultSearchFields.ElementsAs(ctx, &cfg.SearchSettings.DefaultSearchFields, false)...)

	if !m.Retention.IsNull() {
		var r retentionModel
		diags.Append(m.Retention.As(ctx, &r, basetypes.ObjectAsOptions{})...)
		cfg.Retention = &apiRetention{
			Period:                   r.Period.ValueString(),
			Schedule:                 r.Schedule.ValueString(),
			EvaluationScheduleJitter: r.EvaluationScheduleJitter.ValueStringPointer(),
		}
	}
	return cfg, diags
}

func rawList(ctx context.Context, l types.List, diags *diag.Diagnostics) []json.RawMessage {
	out := []json.RawMessage{}
	var elems []normalizedValue
	diags.Append(l.ElementsAs(ctx, &elems, false)...)
	for _, e := range elems {
		out = append(out, json.RawMessage(e.ValueString()))
	}
	return out
}

// ---------------------------------------------------------------------------
// API → state
// ---------------------------------------------------------------------------

// fromAPI converts the server's index metadata to a state model. prior is the
// plan (after create/update) or the current state (read); it decides whether
// optional attributes the user left unset stay null when the server reports
// their default value. With explicit, every value is reported, defaults
// included (data source).
func fromAPI(ctx context.Context, meta *client.IndexMetadata, prior IndexModel, explicit bool) (out IndexModel, diags diag.Diagnostics) {
	var cfg apiIndexConfig
	if err := json.Unmarshal(meta.IndexConfig, &cfg); err != nil {
		diags.AddError("Unexpected index config", fmt.Sprintf("decoding index_config: %s", err))
		return IndexModel{}, diags
	}
	out = IndexModel{
		ID:       types.StringValue(cfg.IndexID),
		IndexID:  types.StringValue(cfg.IndexID),
		IndexURI: types.StringValue(cfg.IndexURI),
		IndexUID: types.StringValue(meta.IndexUID),
	}

	// doc_mapping
	var priorDM docMappingModel
	priorDM.DynamicMapping = newNormalizedNull(kindDynamicMapping)
	if !prior.DocMapping.IsNull() && !prior.DocMapping.IsUnknown() {
		diags.Append(prior.DocMapping.As(ctx, &priorDM, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
	}
	dm := cfg.DocMapping
	dmModel := docMappingModel{
		Mode:               types.StringValue(dm.Mode),
		DynamicMapping:     newNormalizedNull(kindDynamicMapping),
		FieldMappings:      normalizedList(fieldMappingType, dm.FieldMappings, &diags),
		TimestampField:     types.StringPointerValue(dm.TimestampField),
		PartitionKey:       types.StringPointerValue(dm.PartitionKey),
		MaxNumPartitions:   types.Int64Value(dm.MaxNumPartitions),
		IndexFieldPresence: types.BoolValue(dm.IndexFieldPresence),
		StoreDocumentSize:  types.BoolValue(dm.StoreDocumentSize),
		StoreSource:        types.BoolValue(dm.StoreSource),
		Tokenizers:         normalizedList(tokenizerType, dm.Tokenizers, &diags),
		DocMappingUID:      types.StringValue(dm.DocMappingUID),
	}
	if len(dm.DynamicMapping) > 0 && string(dm.DynamicMapping) != "null" {
		s, err := mapping.SortedJSON(string(dm.DynamicMapping))
		if err != nil {
			diags.AddError("Unexpected dynamic_mapping", err.Error())
		}
		canon, _ := mapping.CanonicalizeDynamicMapping(s)
		if explicit || !priorDM.DynamicMapping.IsNull() || canon != mapping.DefaultDynamicMapping() {
			dmModel.DynamicMapping = newNormalized(kindDynamicMapping, s)
		}
	}
	tagFields := dm.TagFields
	if tagFields == nil {
		tagFields = []string{}
	}
	var d diag.Diagnostics
	dmModel.TagFields, d = types.SetValueFrom(ctx, types.StringType, tagFields)
	diags.Append(d...)
	out.DocMapping, d = types.ObjectValueFrom(ctx, objTypes.docMapping, dmModel)
	diags.Append(d...)

	// indexing_settings
	var priorMP mergePolicyModel
	priorMP.MaturationPeriod = newNormalizedNull(kindDuration)
	if !prior.IndexingSettings.IsNull() && !prior.IndexingSettings.IsUnknown() {
		var priorIS indexingSettingsModel
		diags.Append(prior.IndexingSettings.As(ctx, &priorIS, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
		if !priorIS.MergePolicy.IsNull() && !priorIS.MergePolicy.IsUnknown() {
			diags.Append(priorIS.MergePolicy.As(ctx, &priorMP, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
		}
	}
	is := cfg.IndexingSettings
	heap, err := formatByteSize(is.Resources.HeapSize)
	if err != nil {
		diags.AddError("Unexpected heap_size", err.Error())
	}
	mp := flattenMergePolicy(is.MergePolicy, priorMP, explicit, &diags)
	mpObj, d := types.ObjectValueFrom(ctx, objTypes.mergePolicy, mp)
	diags.Append(d...)
	out.IndexingSettings, d = types.ObjectValueFrom(ctx, objTypes.indexing, indexingSettingsModel{
		CommitTimeoutSecs:        types.Int64Value(is.CommitTimeoutSecs),
		DocstoreCompressionLevel: types.Int64Value(is.DocstoreCompressionLevel),
		DocstoreBlocksize:        types.Int64Value(is.DocstoreBlocksize),
		SplitNumDocsTarget:       types.Int64Value(is.SplitNumDocsTarget),
		HeapSize:                 newNormalized(kindByteSize, heap),
		MergePolicy:              mpObj,
	})
	diags.Append(d...)

	// ingest_settings: validate_docs is omitted by the server when true.
	validateDocs := true
	if cfg.IngestSettings.ValidateDocs != nil {
		validateDocs = *cfg.IngestSettings.ValidateDocs
	}
	out.IngestSettings, d = types.ObjectValueFrom(ctx, objTypes.ingest, ingestSettingsModel{
		MinShards: types.Int64Value(cfg.IngestSettings.MinShards), ValidateDocs: types.BoolValue(validateDocs),
	})
	diags.Append(d...)

	searchFields := cfg.SearchSettings.DefaultSearchFields
	if searchFields == nil {
		searchFields = []string{}
	}
	sf, d := types.ListValueFrom(ctx, types.StringType, searchFields)
	diags.Append(d...)
	out.SearchSettings, d = types.ObjectValueFrom(ctx, objTypes.search, searchSettingsModel{DefaultSearchFields: sf})
	diags.Append(d...)

	out.Retention = types.ObjectNull(objTypes.retention)
	if r := cfg.Retention; r != nil {
		jitter := newNormalizedNull(kindDuration)
		if r.EvaluationScheduleJitter != nil {
			jitter = newNormalized(kindDuration, *r.EvaluationScheduleJitter)
		}
		out.Retention, d = types.ObjectValueFrom(ctx, objTypes.retention, retentionModel{
			Period: types.StringValue(r.Period), Schedule: types.StringValue(r.Schedule), EvaluationScheduleJitter: jitter,
		})
		diags.Append(d...)
	}
	return out, diags
}

func normalizedList(t normalizedType, raws []json.RawMessage, diags *diag.Diagnostics) types.List {
	elems := make([]attr.Value, 0, len(raws))
	for _, raw := range raws {
		s, err := mapping.SortedJSON(string(raw))
		if err != nil {
			diags.AddError("Unexpected JSON from Quickwit", err.Error())
			continue
		}
		elems = append(elems, newNormalized(t.kind, s))
	}
	l, d := types.ListValue(t, elems)
	diags.Append(d...)
	return l
}

// flattenMergePolicy keeps an option null when the user did not set it and
// the server reports the policy's default for it.
func flattenMergePolicy(api map[string]any, prior mergePolicyModel, explicit bool, diags *diag.Diagnostics) mergePolicyModel {
	typ, _ := api["type"].(string)
	defaults := mergePolicyDefaults[typ]
	out := mergePolicyModel{Type: types.StringValue(typ), MaturationPeriod: newNormalizedNull(kindDuration)}
	priorInts := prior.ints()
	for k, dst := range out.ints() {
		*dst = types.Int64Null()
		v, ok := api[k]
		if !ok || v == nil {
			continue
		}
		f, ok := v.(float64)
		if !ok {
			diags.AddError("Unexpected merge_policy", fmt.Sprintf("%s: expected a number, got %v", k, v))
			continue
		}
		n := int64(f)
		if !explicit && priorInts[k].IsNull() && defaults[k] == n {
			continue
		}
		*dst = types.Int64Value(n)
	}
	if v, ok := api["maturation_period"].(string); ok {
		// A policy without a default (no_merge, or a type unknown to this
		// provider) always reports the value it got.
		isDefault := false
		if def, ok := defaults["maturation_period"].(string); ok {
			d1, err1 := mapping.ParseHumanDuration(v)
			d2, err2 := mapping.ParseHumanDuration(def)
			isDefault = err1 == nil && err2 == nil && d1 == d2
		}
		if explicit || !prior.MaturationPeriod.IsNull() || !isDefault {
			out.MaturationPeriod = newNormalized(kindDuration, v)
		}
	}
	return out
}

// formatByteSize renders the server's byte count with the largest exact
// decimal unit, so the 2GB default round-trips as "2GB".
func formatByteSize(raw json.RawMessage) (string, error) {
	var n uint64
	if err := json.Unmarshal(raw, &n); err != nil {
		var s string
		if err2 := json.Unmarshal(raw, &s); err2 != nil {
			return "", fmt.Errorf("heap_size: %s", raw)
		}
		return s, nil
	}
	for _, u := range []struct {
		suffix string
		size   uint64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}} {
		if n >= u.size && n%u.size == 0 {
			return strconv.FormatUint(n/u.size, 10) + u.suffix, nil
		}
	}
	return strconv.FormatUint(n, 10), nil
}
