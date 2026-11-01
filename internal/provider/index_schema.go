package provider

import (
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Server-side defaults (quickwit-config/src/index_config/mod.rs).
const (
	defaultMode                     = "dynamic"
	defaultMaxNumPartitions         = 200
	defaultCommitTimeoutSecs        = 60
	defaultDocstoreCompressionLevel = 8
	defaultDocstoreBlocksize        = 1_000_000
	defaultSplitNumDocsTarget       = 10_000_000
	defaultHeapSize                 = "2GB"
	defaultMergePolicy              = "stable_log"
	defaultMinShards                = 1
	defaultRetentionSchedule        = "hourly"
)

var (
	indexIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-_\.]{2,254}$`)
	mergePolicies  = []string{"stable_log", "limit_merge", "no_merge"}
	docModes       = []string{"dynamic", "lenient", "strict"}
)

// mergePolicyDefaults are the server defaults of each merge policy option,
// per policy type; an option absent from a policy's map is not accepted by it.
var mergePolicyDefaults = map[string]map[string]any{
	"stable_log": {
		"min_level_num_docs": int64(100_000), "merge_factor": int64(10), "max_merge_factor": int64(12),
		"maturation_period": "2days",
	},
	"limit_merge": {
		"merge_factor": int64(10), "max_merge_factor": int64(12), "max_merge_ops": int64(4),
		"maturation_period": "2days", "max_finalize_merge_operations": int64(0), "max_finalize_split_num_docs": nil,
	},
	"no_merge": {},
}

var mergePolicyIntOptions = []string{"min_level_num_docs", "merge_factor", "max_merge_factor", "max_merge_ops", "max_finalize_merge_operations", "max_finalize_split_num_docs"}

func indexSchema() schema.Schema {
	mergePolicyAttrs := map[string]schema.Attribute{
		"type": schema.StringAttribute{
			Optional: true, Computed: true, Default: stringdefault.StaticString(defaultMergePolicy),
			Validators:          []validator.String{stringvalidator.OneOf(mergePolicies...)},
			MarkdownDescription: "Merge policy: `stable_log` (default), `limit_merge` or `no_merge`.",
		},
		"min_level_num_docs":            optInt("stable_log: number of docs below which all splits are considered on the same level. Server default `100000`."),
		"merge_factor":                  optInt("stable_log, limit_merge: number of splits merged together. Server default `10`."),
		"max_merge_factor":              optInt("stable_log, limit_merge: maximum number of splits merged together. Server default `12`."),
		"max_merge_ops":                 optInt("limit_merge: maximum number of merges a document goes through. Server default `4`."),
		"max_finalize_merge_operations": optInt("limit_merge: merges allowed when finalizing a source. Server default `0`."),
		"max_finalize_split_num_docs":   optInt("limit_merge: maximum number of docs of splits merged when finalizing a source."),
		"maturation_period": schema.StringAttribute{
			Optional: true, CustomType: durationType,
			MarkdownDescription: "stable_log, limit_merge: time after which a split is mature and no longer merged (`48h`, `2days`). Server default `2days`.",
		},
	}

	return schema.Schema{
		MarkdownDescription: "A Quickwit index. The whole configuration is sent with every create (`POST /indexes`) and update (`PUT /indexes/{id}`). " +
			"Build `field_mappings` and `tokenizers` with the `quickwit_field_*` and `quickwit_tokenizer` data sources.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Same as `index_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"index_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "Index identifier. Changing it recreates the index.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(indexIDPattern,
					"must start with a letter and contain only letters, digits, `-`, `_` and `.` (3 to 255 characters)")},
			},
			"index_uri": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Storage URI of the index (`s3://bucket/indexes/foo`). Defaults to the node's `default_index_root_uri` joined with `index_id`. Changing it recreates the index.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"index_uid": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Index UID (`index_id:ulid`), new for each incarnation of the index.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"doc_mapping": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "How documents are parsed, indexed and stored.",
				Attributes: map[string]schema.Attribute{
					"mode": schema.StringAttribute{
						Optional: true, Computed: true, Default: stringdefault.StaticString(defaultMode),
						Validators:          []validator.String{stringvalidator.OneOf(docModes...)},
						MarkdownDescription: "What to do with unmapped fields: `dynamic` (index them with `dynamic_mapping`, default), `lenient` (ignore) or `strict` (reject the document).",
					},
					"dynamic_mapping": schema.StringAttribute{
						Optional: true, CustomType: dynamicMappingType,
						MarkdownDescription: "JSON options applied to unmapped fields in `dynamic` mode (same options as a `json` field: `indexed`, `stored`, `tokenizer`, `record`, `expand_dots`, `fast`). Only allowed with `mode = \"dynamic\"`.",
					},
					"field_mappings": schema.ListAttribute{
						Optional: true, Computed: true, ElementType: fieldMappingType,
						Default:             listdefault.StaticValue(types.ListValueMust(fieldMappingType, []attr.Value{})),
						MarkdownDescription: "Field mappings, as JSON documents. Use the `json` output of `quickwit_field_*` data sources. Compared semantically: defaults and key order never cause diffs.",
					},
					"timestamp_field": schema.StringAttribute{
						Optional: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
						MarkdownDescription: "Name of a single-valued fast `datetime` field used for time pruning and retention. Changing it recreates the index.",
					},
					"tag_fields": schema.SetAttribute{
						Optional: true, Computed: true, ElementType: types.StringType,
						Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
						MarkdownDescription: "Low-cardinality fields used to prune splits at search time (text fields with the `raw` tokenizer, `i64` or `u64`).",
					},
					"partition_key": schema.StringAttribute{
						Optional: true, MarkdownDescription: "Partitioning expression, e.g. `tenant_id` or `hash_mod(service, 10)`.",
					},
					"max_num_partitions": schema.Int64Attribute{
						Optional: true, Computed: true, Default: int64default.StaticInt64(defaultMaxNumPartitions),
						Validators:          []validator.Int64{int64validator.AtLeast(1)},
						MarkdownDescription: "Maximum number of partitions per indexer. Defaults to `200`.",
					},
					"index_field_presence": optBool("Index field presence, for `exists` queries on non-fast fields. Defaults to `false`."),
					"store_document_size":  optBool("Store the size of each document in `_doc_length`. Defaults to `false`."),
					"store_source":         optBool("Store the original document in `_source`. Defaults to `false`."),
					"tokenizers": schema.ListAttribute{
						Optional: true, Computed: true, ElementType: tokenizerType,
						Default:             listdefault.StaticValue(types.ListValueMust(tokenizerType, []attr.Value{})),
						MarkdownDescription: "Custom tokenizers, as JSON documents (`data.quickwit_tokenizer.x.json`). Tokenizers can only be added, never removed or changed, without recreating the index.",
					},
					"doc_mapping_uid": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "UID of the current doc mapping. Quickwit generates a new one each time the mapping changes.",
					},
				},
			},
			"indexing_settings": schema.SingleNestedAttribute{
				Optional: true, Computed: true,
				Default:             objectdefault.StaticValue(defaultIndexingSettings(mergePolicyAttrs)),
				MarkdownDescription: "Indexing settings.",
				Attributes: map[string]schema.Attribute{
					"commit_timeout_secs":        defInt(defaultCommitTimeoutSecs, "Maximum time, in seconds, before a document becomes searchable. Defaults to `60`."),
					"docstore_compression_level": defInt(defaultDocstoreCompressionLevel, "zstd compression level of the doc store. Defaults to `8`."),
					"docstore_blocksize":         defInt(defaultDocstoreBlocksize, "Size, in bytes, of doc store blocks. Defaults to `1000000`."),
					"split_num_docs_target":      defInt(defaultSplitNumDocsTarget, "Target number of documents per split. Defaults to `10000000`."),
					"heap_size": schema.StringAttribute{
						Optional: true, Computed: true, CustomType: byteSizeType,
						Default:             stringdefault.StaticString(defaultHeapSize),
						MarkdownDescription: "Indexer heap size per pipeline (`resources.heap_size`), e.g. `2GB`, `500MiB`. Defaults to `2GB`.",
					},
					"merge_policy": schema.SingleNestedAttribute{
						Optional: true, Computed: true,
						Default:             objectdefault.StaticValue(defaultMergePolicyObject(mergePolicyAttrs)),
						MarkdownDescription: "Merge policy. Options left unset use the server defaults of the chosen `type`.",
						Attributes:          mergePolicyAttrs,
					},
				},
			},
			"ingest_settings": schema.SingleNestedAttribute{
				Optional: true, Computed: true,
				Default: objectdefault.StaticValue(types.ObjectValueMust(
					map[string]attr.Type{"min_shards": types.Int64Type, "validate_docs": types.BoolType},
					map[string]attr.Value{"min_shards": types.Int64Value(defaultMinShards), "validate_docs": types.BoolValue(true)},
				)),
				MarkdownDescription: "Ingest API settings.",
				Attributes: map[string]schema.Attribute{
					"min_shards": schema.Int64Attribute{
						Optional: true, Computed: true, Default: int64default.StaticInt64(defaultMinShards),
						Validators:          []validator.Int64{int64validator.AtLeast(1)},
						MarkdownDescription: "Minimum number of ingest shards. Defaults to `1`.",
					},
					"validate_docs": schema.BoolAttribute{
						Optional: true, Computed: true, Default: booldefault.StaticBool(true),
						MarkdownDescription: "Validate documents against the doc mapping at ingest time. Defaults to `true`.",
					},
				},
			},
			"search_settings": schema.SingleNestedAttribute{
				Optional: true, Computed: true,
				Default: objectdefault.StaticValue(types.ObjectValueMust(
					map[string]attr.Type{"default_search_fields": types.ListType{ElemType: types.StringType}},
					map[string]attr.Value{"default_search_fields": types.ListValueMust(types.StringType, []attr.Value{})},
				)),
				MarkdownDescription: "Search settings.",
				Attributes: map[string]schema.Attribute{
					"default_search_fields": schema.ListAttribute{
						Optional: true, Computed: true, ElementType: types.StringType,
						Default:             listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})),
						MarkdownDescription: "Fields searched when a query does not name one.",
					},
				},
			},
			"retention": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Retention policy: splits older than `period` are deleted. Requires `doc_mapping.timestamp_field`. Removing the block removes the policy.",
				Attributes: map[string]schema.Attribute{
					"period": schema.StringAttribute{
						Required: true, MarkdownDescription: "Retention period, e.g. `30 days`, `1 week`.",
					},
					"schedule": schema.StringAttribute{
						Optional: true, Computed: true, Default: stringdefault.StaticString(defaultRetentionSchedule),
						MarkdownDescription: "When the policy is evaluated: `hourly`, `daily`, `weekly`, `monthly` or a cron expression. Defaults to `hourly`.",
					},
					"evaluation_schedule_jitter": schema.StringAttribute{
						Optional: true, CustomType: durationType,
						MarkdownDescription: "Maximum random delay added to each evaluation, e.g. `10min`.",
					},
				},
			},
		},
	}
}

func optInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{Optional: true, MarkdownDescription: desc, Validators: []validator.Int64{int64validator.AtLeast(0)}}
}

func defInt(def int64, desc string) schema.Int64Attribute {
	return schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(def), MarkdownDescription: desc}
}

func optBool(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), MarkdownDescription: desc}
}

func attrTypes(attrs map[string]schema.Attribute) map[string]attr.Type {
	out := make(map[string]attr.Type, len(attrs))
	for k, a := range attrs {
		out[k] = a.GetType()
	}
	return out
}

func defaultMergePolicyObject(attrs map[string]schema.Attribute) types.Object {
	vals := map[string]attr.Value{
		"type":              types.StringValue(defaultMergePolicy),
		"maturation_period": newNormalizedNull(kindDuration),
	}
	for _, k := range mergePolicyIntOptions {
		vals[k] = types.Int64Null()
	}
	return types.ObjectValueMust(attrTypes(attrs), vals)
}

func defaultIndexingSettings(mergePolicyAttrs map[string]schema.Attribute) types.Object {
	return types.ObjectValueMust(
		map[string]attr.Type{
			"commit_timeout_secs": types.Int64Type, "docstore_compression_level": types.Int64Type,
			"docstore_blocksize": types.Int64Type, "split_num_docs_target": types.Int64Type,
			"heap_size": byteSizeType, "merge_policy": types.ObjectType{AttrTypes: attrTypes(mergePolicyAttrs)},
		},
		map[string]attr.Value{
			"commit_timeout_secs":        types.Int64Value(defaultCommitTimeoutSecs),
			"docstore_compression_level": types.Int64Value(defaultDocstoreCompressionLevel),
			"docstore_blocksize":         types.Int64Value(defaultDocstoreBlocksize),
			"split_num_docs_target":      types.Int64Value(defaultSplitNumDocsTarget),
			"heap_size":                  newNormalized(kindByteSize, defaultHeapSize),
			"merge_policy":               defaultMergePolicyObject(mergePolicyAttrs),
		},
	)
}
