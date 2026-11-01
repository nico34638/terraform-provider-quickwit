package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/nico34638/terraform-provider-quickwit/internal/client"
)

// Sources follow the one-resource-per-API-object pattern: each
// quickwit_source_<type> resource maps to /indexes/{index_id}/sources/{source_id}
// and is updated with a full PUT. The code shared by all source types lives
// here; each type provides its own model (embedding SourceCommon) and the
// conversion of its `params` object.

var (
	// RESERVED_SOURCE_IDS in quickwit-config/src/source_config/mod.rs.
	reservedSourceIDs = []string{"_ingest-api-source", "_ingest-cli-source", "_ingest-source"}
	// validate_identifier in quickwit-config/src/lib.rs.
	identifierPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-_\.]{2,254}$`)
	// SourceInputFormat (canonical spellings only, aliases would cause diffs).
	sourceInputFormats = []string{"json", "plain_text", "otlp_logs_json", "otlp_logs_protobuf", "otlp_traces_json", "otlp_traces_protobuf"}
)

const defaultTimezone = "UTC"

// SourceCommon holds the attributes shared by every source type. It must be
// exported: the framework only flattens exported embedded structs.
type SourceCommon struct {
	ID           types.String `tfsdk:"id"`
	IndexID      types.String `tfsdk:"index_id"`
	SourceID     types.String `tfsdk:"source_id"`
	NumPipelines types.Int64  `tfsdk:"num_pipelines"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	InputFormat  types.String `tfsdk:"input_format"`
	Transform    types.Object `tfsdk:"transform"`
}

type transformModel struct {
	Script   types.String `tfsdk:"script"`
	Timezone types.String `tfsdk:"timezone"`
}

var transformAttrTypes = map[string]attr.Type{"script": types.StringType, "timezone": types.StringType}

// sourceModel is implemented by the pointer to each per-type model.
type sourceModel interface {
	common() *SourceCommon
	// params returns the `params` object sent to Quickwit.
	params(ctx context.Context) (any, diag.Diagnostics)
	// setParams fills the type-specific attributes from the server's params.
	setParams(ctx context.Context, raw json.RawMessage) diag.Diagnostics
}

type sourceSpec struct {
	name              string // resource suffix: quickwit_source_<name>
	sourceType        string // Quickwit source_type
	desc              string
	multiplePipelines bool
	attrs             map[string]schema.Attribute
	// matches tells whether a server source of sourceType is of this kind
	// (file sources can be path- or notification-based).
	matches func(raw json.RawMessage) bool
}

type sourceResource[T any, M interface {
	*T
	sourceModel
}] struct {
	spec   sourceSpec
	client *client.Client
}

var (
	_ resource.ResourceWithConfigure      = (*sourceResource[kafkaModel, *kafkaModel])(nil)
	_ resource.ResourceWithImportState    = (*sourceResource[kafkaModel, *kafkaModel])(nil)
	_ resource.ResourceWithValidateConfig = (*sourceResource[kafkaModel, *kafkaModel])(nil)
)

func (r *sourceResource[T, M]) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_source_" + r.spec.name
}

func (r *sourceResource[T, M]) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	numPipelinesValidators := []validator.Int64{int64validator.AtLeast(1)}
	numPipelinesDesc := "Number of indexing pipelines. Defaults to `1`."
	if !r.spec.multiplePipelines {
		numPipelinesValidators = append(numPipelinesValidators, int64validator.AtMost(1))
		numPipelinesDesc = "Number of indexing pipelines. Quickwit only supports `1` for this source type."
	}
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed: true, MarkdownDescription: "`<index_id>:<source_id>`.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"index_id": schema.StringAttribute{
			Required: true, MarkdownDescription: "Index the source feeds. Changing it recreates the source.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"source_id": schema.StringAttribute{
			Required: true, MarkdownDescription: "Source identifier, unique within the index. Changing it recreates the source.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			Validators: []validator.String{
				stringvalidator.NoneOf(reservedSourceIDs...),
				stringvalidator.RegexMatches(identifierPattern, "must start with a letter and contain only letters, digits, `-`, `_` and `.` (3 to 255 characters)"),
			},
		},
		"num_pipelines": schema.Int64Attribute{
			Optional: true, Computed: true, Default: int64default.StaticInt64(1),
			Validators: numPipelinesValidators, MarkdownDescription: numPipelinesDesc,
		},
		"enabled": schema.BoolAttribute{
			Optional: true, Computed: true, Default: booldefault.StaticBool(true),
			MarkdownDescription: "Whether the source is indexed. Defaults to `true`.",
		},
		"input_format": schema.StringAttribute{
			Optional: true, Computed: true, Default: stringdefault.StaticString("json"),
			Validators:          []validator.String{stringvalidator.OneOf(sourceInputFormats...)},
			MarkdownDescription: "Format of the messages: `" + strings.Join(sourceInputFormats, "`, `") + "`. Defaults to `json`.",
		},
		"transform": schema.SingleNestedAttribute{
			Optional:            true,
			MarkdownDescription: "[VRL](https://vector.dev/docs/reference/vrl/) transform applied to each document before indexing. Not supported with OTLP input formats.",
			Attributes: map[string]schema.Attribute{
				"script": schema.StringAttribute{Required: true, MarkdownDescription: "VRL program, e.g. `.message = downcase(string!(.message))`."},
				"timezone": schema.StringAttribute{
					Optional: true, Computed: true, Default: stringdefault.StaticString(defaultTimezone),
					MarkdownDescription: "TZ database timezone used by VRL time functions. Defaults to `UTC`.",
				},
			},
		},
	}
	for k, v := range r.spec.attrs {
		attrs[k] = v
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: r.spec.desc + "\n\nQuickwit checks that it can reach the source when it is created or updated. " +
			"Sources are deleted together with their index: when the index is replaced, add " +
			"`lifecycle { replace_triggered_by = [quickwit_index.<name>] }` so the source is recreated in the same apply.",
		Attributes: attrs,
	}
}

func (r *sourceResource[T, M]) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *sourceResource[T, M]) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var format types.String
	var transform types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("input_format"), &format)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("transform"), &transform)...)
	if !transform.IsNull() && strings.HasPrefix(format.ValueString(), "otlp_") {
		resp.Diagnostics.AddAttributeError(path.Root("transform"), "Transform not supported",
			"VRL transforms are not supported for OTLP input formats")
	}
}

func (r *sourceResource[T, M]) toAPI(ctx context.Context, m M) (*client.Source, diag.Diagnostics) {
	c := m.common()
	params, diags := m.params(ctx)
	raw, err := json.Marshal(params)
	if err != nil {
		diags.AddError("Invalid source params", err.Error())
	}
	src := &client.Source{
		Version:      "0.9",
		SourceID:     c.SourceID.ValueString(),
		NumPipelines: c.NumPipelines.ValueInt64(),
		Enabled:      c.Enabled.ValueBool(),
		SourceType:   r.spec.sourceType,
		Params:       raw,
		InputFormat:  c.InputFormat.ValueString(),
	}
	if !c.Transform.IsNull() && !c.Transform.IsUnknown() {
		var t transformModel
		diags.Append(c.Transform.As(ctx, &t, basetypes.ObjectAsOptions{})...)
		src.Transform = &client.Transform{Script: t.Script.ValueString(), Timezone: t.Timezone.ValueString()}
	}
	return src, diags
}

// fromAPI overwrites m with the server's view of the source.
func (r *sourceResource[T, M]) fromAPI(ctx context.Context, indexID string, src *client.Source, m M) (diags diag.Diagnostics) {
	if src.SourceType != r.spec.sourceType || (r.spec.matches != nil && !r.spec.matches(src.Params)) {
		diags.AddError("Wrong source type",
			fmt.Sprintf("source `%s` of index `%s` is a `%s` source, not a quickwit_source_%s", src.SourceID, indexID, src.SourceType, r.spec.name))
		return diags
	}
	c := m.common()
	c.ID = types.StringValue(indexID + ":" + src.SourceID)
	c.IndexID = types.StringValue(indexID)
	c.SourceID = types.StringValue(src.SourceID)
	c.NumPipelines = types.Int64Value(src.NumPipelines)
	c.Enabled = types.BoolValue(src.Enabled)
	c.InputFormat = types.StringValue(src.InputFormat)
	c.Transform = types.ObjectNull(transformAttrTypes)
	if t := src.Transform; t != nil {
		tz := t.Timezone
		if tz == "" {
			tz = defaultTimezone
		}
		var d diag.Diagnostics
		c.Transform, d = types.ObjectValueFrom(ctx, transformAttrTypes, transformModel{Script: types.StringValue(t.Script), Timezone: types.StringValue(tz)})
		diags.Append(d...)
	}
	diags.Append(m.setParams(ctx, src.Params)...)
	return diags
}

func (r *sourceResource[T, M]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !configured(r.client, &resp.Diagnostics) {
		return
	}
	m := M(new(T))
	resp.Diagnostics.Append(req.Plan.Get(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	src, diags := r.toAPI(ctx, m)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	indexID := m.common().IndexID.ValueString()
	out, err := r.client.CreateSource(ctx, indexID, src)
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Failed to create source %s of index %s", src.SourceID, indexID), err.Error())
		return
	}
	resp.Diagnostics.Append(r.fromAPI(ctx, indexID, out, m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *sourceResource[T, M]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !configured(r.client, &resp.Diagnostics) {
		return
	}
	m := M(new(T))
	resp.Diagnostics.Append(req.State.Get(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := m.common()
	out, err := r.client.GetSource(ctx, c.IndexID.ValueString(), c.SourceID.ValueString())
	if client.IsNotFound(err) { // source or index gone
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Failed to read source "+c.ID.ValueString(), err.Error())
		return
	}
	resp.Diagnostics.Append(r.fromAPI(ctx, c.IndexID.ValueString(), out, m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *sourceResource[T, M]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if !configured(r.client, &resp.Diagnostics) {
		return
	}
	m := M(new(T))
	resp.Diagnostics.Append(req.Plan.Get(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	src, diags := r.toAPI(ctx, m)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	indexID := m.common().IndexID.ValueString()
	out, err := r.client.UpdateSource(ctx, indexID, src)
	if err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Failed to update source %s of index %s", src.SourceID, indexID), err.Error())
		return
	}
	resp.Diagnostics.Append(r.fromAPI(ctx, indexID, out, m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *sourceResource[T, M]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !configured(r.client, &resp.Diagnostics) {
		return
	}
	m := M(new(T))
	resp.Diagnostics.Append(req.State.Get(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := m.common()
	err := r.client.DeleteSource(ctx, c.IndexID.ValueString(), c.SourceID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Failed to delete source "+c.ID.ValueString(), err.Error())
	}
}

func (r *sourceResource[T, M]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	indexID, sourceID, ok := strings.Cut(req.ID, ":")
	if !ok || indexID == "" || sourceID == "" {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("expected `<index_id>:<source_id>`, got %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("index_id"), indexID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("source_id"), sourceID)...)
}

func sourceResources() []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return &sourceResource[kafkaModel, *kafkaModel]{spec: kafkaSpec} },
		func() resource.Resource { return &sourceResource[kinesisModel, *kinesisModel]{spec: kinesisSpec} },
		func() resource.Resource { return &sourceResource[pulsarModel, *pulsarModel]{spec: pulsarSpec} },
		func() resource.Resource { return &sourceResource[pubsubModel, *pubsubModel]{spec: pubsubSpec} },
		func() resource.Resource { return &sourceResource[sqsModel, *sqsModel]{spec: sqsSpec} },
	}
}
