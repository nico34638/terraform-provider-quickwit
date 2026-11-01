package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nico34638/terraform-provider-quickwit/internal/client"
)

var (
	_ datasource.DataSource              = (*indexDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*indexDataSource)(nil)
)

func NewIndexDataSource() datasource.DataSource { return &indexDataSource{} }

type indexDataSource struct {
	client *client.Client
}

type indexDataSourceModel struct {
	IndexModel
	CreateTimestamp types.Int64 `tfsdk:"create_timestamp"`
	Sources         types.List  `tfsdk:"sources"`
}

type indexSourceSummary struct {
	SourceID     types.String `tfsdk:"source_id"`
	SourceType   types.String `tfsdk:"source_type"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	NumPipelines types.Int64  `tfsdk:"num_pipelines"`
	InputFormat  types.String `tfsdk:"input_format"`
}

var indexSourceSummaryAttrTypes = map[string]attr.Type{
	"source_id": types.StringType, "source_type": types.StringType, "enabled": types.BoolType,
	"num_pipelines": types.Int64Type, "input_format": types.StringType,
}

func (d *indexDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_index"
}

func (d *indexDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := computedAttributes(indexSchema().Attributes)
	attrs["index_id"] = dschema.StringAttribute{Required: true, MarkdownDescription: "Identifier of the index to read."}
	attrs["create_timestamp"] = dschema.Int64Attribute{Computed: true, MarkdownDescription: "Creation time of the index, in seconds since the Unix epoch."}
	attrs["sources"] = dschema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Sources of the index, sorted by `source_id`, including the built-in `_ingest-*` sources.",
		NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
			"source_id":     dschema.StringAttribute{Computed: true, MarkdownDescription: "Source identifier."},
			"source_type":   dschema.StringAttribute{Computed: true, MarkdownDescription: "Source type (`kafka`, `file`, `ingest`…)."},
			"enabled":       dschema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the source is enabled."},
			"num_pipelines": dschema.Int64Attribute{Computed: true, MarkdownDescription: "Number of indexing pipelines."},
			"input_format":  dschema.StringAttribute{Computed: true, MarkdownDescription: "Message format."},
		}},
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads an existing Quickwit index. Unlike the `quickwit_index` resource, every setting is reported, server defaults included. " +
			"`field_mappings` and `tokenizers` are the canonical JSON documents returned by Quickwit.",
		Attributes: attrs,
	}
}

// computedAttributes turns the resource schema into an all-computed data
// source schema, so that both always expose the same attributes.
func computedAttributes(in map[string]rschema.Attribute) map[string]dschema.Attribute {
	out := make(map[string]dschema.Attribute, len(in))
	for name, a := range in {
		desc := a.GetMarkdownDescription()
		switch a := a.(type) {
		case rschema.StringAttribute:
			out[name] = dschema.StringAttribute{Computed: true, CustomType: a.CustomType, Sensitive: a.Sensitive, MarkdownDescription: desc}
		case rschema.Int64Attribute:
			out[name] = dschema.Int64Attribute{Computed: true, MarkdownDescription: desc}
		case rschema.BoolAttribute:
			out[name] = dschema.BoolAttribute{Computed: true, MarkdownDescription: desc}
		case rschema.ListAttribute:
			out[name] = dschema.ListAttribute{Computed: true, ElementType: a.ElementType, MarkdownDescription: desc}
		case rschema.SetAttribute:
			out[name] = dschema.SetAttribute{Computed: true, ElementType: a.ElementType, MarkdownDescription: desc}
		case rschema.SingleNestedAttribute:
			out[name] = dschema.SingleNestedAttribute{Computed: true, Attributes: computedAttributes(a.Attributes), MarkdownDescription: desc}
		default:
			panic(fmt.Sprintf("computedAttributes: unsupported attribute %s (%T)", name, a))
		}
	}
	return out
}

func (d *indexDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}

func (d *indexDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var indexID types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("index_id"), &indexID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	meta, err := d.client.GetIndex(ctx, indexID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read index "+indexID.ValueString(), err.Error())
		return
	}
	index, diags := fromAPI(ctx, meta, IndexModel{}, true)
	resp.Diagnostics.Append(diags...)

	summaries := make([]indexSourceSummary, 0, len(meta.Sources))
	for _, raw := range meta.Sources {
		var s client.Source
		if err := json.Unmarshal(raw, &s); err != nil {
			resp.Diagnostics.AddError("Unexpected source from Quickwit", err.Error())
			continue
		}
		summaries = append(summaries, indexSourceSummary{
			SourceID: types.StringValue(s.SourceID), SourceType: types.StringValue(s.SourceType),
			Enabled: types.BoolValue(s.Enabled), NumPipelines: types.Int64Value(s.NumPipelines),
			InputFormat: types.StringValue(s.InputFormat),
		})
	}
	// Quickwit returns sources in no particular order.
	slices.SortFunc(summaries, func(a, b indexSourceSummary) int {
		return strings.Compare(a.SourceID.ValueString(), b.SourceID.ValueString())
	})
	sources, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: indexSourceSummaryAttrTypes}, summaries)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &indexDataSourceModel{
		IndexModel:      index,
		CreateTimestamp: types.Int64Value(meta.CreateTimestamp),
		Sources:         sources,
	})...)
}
