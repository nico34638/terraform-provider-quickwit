package provider

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/nico34638/terraform-provider-quickwit/internal/client"
	"github.com/nico34638/terraform-provider-quickwit/internal/mapping"
)

var (
	_ resource.Resource                = (*indexResource)(nil)
	_ resource.ResourceWithConfigure   = (*indexResource)(nil)
	_ resource.ResourceWithImportState = (*indexResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*indexResource)(nil)
)

func NewIndexResource() resource.Resource { return &indexResource{} }

type indexResource struct {
	client *client.Client
}

func (r *indexResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_index"
}

func (r *indexResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = indexSchema()
}

func (r *indexResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *indexResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !configured(r.client, &resp.Diagnostics) {
		return
	}
	var plan IndexModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cfg, diags := toAPI(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	meta, err := r.client.CreateIndex(ctx, cfg)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create index "+cfg.IndexID, err.Error())
		return
	}
	state, diags := fromAPI(ctx, meta, plan, false)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *indexResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !configured(r.client, &resp.Diagnostics) {
		return
	}
	var prior IndexModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	meta, err := r.client.GetIndex(ctx, prior.IndexID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Failed to read index "+prior.IndexID.ValueString(), err.Error())
		return
	}
	state, diags := fromAPI(ctx, meta, prior, false)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *indexResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if !configured(r.client, &resp.Diagnostics) {
		return
	}
	var plan IndexModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cfg, diags := toAPI(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	meta, err := r.client.UpdateIndex(ctx, cfg.IndexID, cfg)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update index "+cfg.IndexID, err.Error())
		return
	}
	state, diags := fromAPI(ctx, meta, plan, false)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *indexResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !configured(r.client, &resp.Diagnostics) {
		return
	}
	var state IndexModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteIndex(ctx, state.IndexID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Failed to delete index "+state.IndexID.ValueString(), err.Error())
	}
}

func (r *indexResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("index_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ModifyPlan rejects at plan time what Quickwit would reject at apply time,
// and keeps doc_mapping_uid known when the doc mapping does not change.
func (r *indexResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroy
	}
	var plan IndexModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.DocMapping.IsUnknown() {
		return
	}
	var dm docMappingModel
	resp.Diagnostics.Append(plan.DocMapping.As(ctx, &dm, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
	if resp.Diagnostics.HasError() {
		return
	}

	dmPath := path.Root("doc_mapping")
	resp.Diagnostics.Append(validateDocMapping(ctx, dm, dmPath)...)
	resp.Diagnostics.Append(validateMergePolicy(ctx, plan.IndexingSettings)...)
	if !plan.Retention.IsNull() && !plan.Retention.IsUnknown() && dm.TimestampField.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("retention"), "Retention requires a timestamp field",
			"a retention policy deletes splits by time: set doc_mapping.timestamp_field.")
	}

	if req.State.Raw.IsNull() {
		return // create
	}
	var state IndexModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var stateDM docMappingModel
	resp.Diagnostics.Append(state.DocMapping.As(ctx, &stateDM, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Tokenizers can only be added.
	if !dm.Tokenizers.IsUnknown() {
		planned := canonicalSet(ctx, dm.Tokenizers, &resp.Diagnostics)
		for _, t := range sortedKeys(canonicalSet(ctx, stateDM.Tokenizers, &resp.Diagnostics)) {
			if !planned[t] {
				resp.Diagnostics.AddAttributeError(dmPath.AtName("tokenizers"), "Tokenizers cannot be removed or changed",
					fmt.Sprintf("Quickwit only allows adding tokenizers to an existing index. Tokenizer %s would be removed or modified; "+
						"keep it, or recreate the index (e.g. `terraform apply -replace`).", t))
			}
		}
	}

	// doc_mapping_uid only changes when the doc mapping does.
	if docMappingUnchanged(dm, stateDM) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, dmPath.AtName("doc_mapping_uid"), stateDM.DocMappingUID)...)
	}
}

func docMappingUnchanged(a, b docMappingModel) bool {
	return a.Mode.Equal(b.Mode) && a.DynamicMapping.Equal(b.DynamicMapping) && a.FieldMappings.Equal(b.FieldMappings) &&
		a.TimestampField.Equal(b.TimestampField) && a.TagFields.Equal(b.TagFields) && a.PartitionKey.Equal(b.PartitionKey) &&
		a.MaxNumPartitions.Equal(b.MaxNumPartitions) && a.IndexFieldPresence.Equal(b.IndexFieldPresence) &&
		a.StoreDocumentSize.Equal(b.StoreDocumentSize) && a.StoreSource.Equal(b.StoreSource) && a.Tokenizers.Equal(b.Tokenizers)
}

func validateDocMapping(ctx context.Context, dm docMappingModel, p path.Path) (diags diag.Diagnostics) {
	if !dm.DynamicMapping.IsNull() && !dm.Mode.IsUnknown() && dm.Mode.ValueString() != "dynamic" {
		diags.AddAttributeError(p.AtName("dynamic_mapping"), "dynamic_mapping requires mode = \"dynamic\"",
			fmt.Sprintf("`dynamic_mapping` is only allowed with mode=dynamic (here mode=%s)", dm.Mode.ValueString()))
	}
	if dm.FieldMappings.IsUnknown() {
		return diags
	}
	var elems []normalizedValue
	diags.Append(dm.FieldMappings.ElementsAs(ctx, &elems, false)...)
	var fields []*mapping.Field
	seen := map[string]bool{}
	for i, e := range elems {
		if e.IsUnknown() {
			return diags // not all fields known yet
		}
		f, err := mapping.ParseField(e.ValueString())
		if err != nil {
			return diags // reported by the attribute validation
		}
		if seen[f.Name] {
			diags.AddAttributeError(p.AtName("field_mappings").AtListIndex(i), "Duplicated field", fmt.Sprintf("field `%s` is defined twice", f.Name))
		}
		seen[f.Name] = true
		fields = append(fields, f)
	}

	if ts := dm.TimestampField; !ts.IsNull() && !ts.IsUnknown() {
		name := ts.ValueString()
		f := mapping.Lookup(fields, name)
		switch {
		case f == nil:
			diags.AddAttributeError(p.AtName("timestamp_field"), "Unknown timestamp field",
				fmt.Sprintf("could not find timestamp field `%s` in field mappings", name))
		case f.Type != mapping.TypeDatetime:
			diags.AddAttributeError(p.AtName("timestamp_field"), "Invalid timestamp field", fmt.Sprintf("timestamp field `%s` should be a datetime field", name))
		case f.Array:
			diags.AddAttributeError(p.AtName("timestamp_field"), "Invalid timestamp field", fmt.Sprintf("timestamp field `%s` should be single-valued", name))
		case f.Options["fast"] != true:
			diags.AddAttributeError(p.AtName("timestamp_field"), "Invalid timestamp field", fmt.Sprintf("timestamp field `%s` should be a fast field", name))
		}
	}

	if !dm.TagFields.IsUnknown() {
		var tags []types.String
		diags.Append(dm.TagFields.ElementsAs(ctx, &tags, false)...)
		for _, t := range tags {
			if t.IsUnknown() {
				continue
			}
			name := t.ValueString()
			f := mapping.Lookup(fields, name)
			tagPath := p.AtName("tag_fields")
			switch {
			case f == nil:
				diags.AddAttributeError(tagPath, "Unknown tag field", fmt.Sprintf("unknown tag field: `%s` is not in field mappings", name))
			case f.Type == mapping.TypeText:
				if f.Options["tokenizer"] != "raw" {
					diags.AddAttributeError(tagPath, "Invalid tag field", fmt.Sprintf("tag field `%s`: tags collection is only allowed on text fields with the `raw` tokenizer", name))
				}
			case f.Type == mapping.TypeI64 || f.Type == mapping.TypeU64:
			default:
				diags.AddAttributeError(tagPath, "Invalid tag field", fmt.Sprintf("tag field `%s`: tags collection is not allowed on `%s` fields", name, f.Type))
			}
		}
	}
	return diags
}

func validateMergePolicy(ctx context.Context, settings types.Object) (diags diag.Diagnostics) {
	if settings.IsNull() || settings.IsUnknown() {
		return nil
	}
	var is indexingSettingsModel
	diags.Append(settings.As(ctx, &is, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
	if diags.HasError() || is.MergePolicy.IsNull() || is.MergePolicy.IsUnknown() {
		return diags
	}
	var mp mergePolicyModel
	diags.Append(is.MergePolicy.As(ctx, &mp, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
	if mp.Type.IsUnknown() {
		return diags
	}
	typ := mp.Type.ValueString()
	allowed := mergePolicyDefaults[typ]
	p := path.Root("indexing_settings").AtName("merge_policy")
	set := map[string]bool{"maturation_period": !mp.MaturationPeriod.IsNull()}
	for k, v := range mp.ints() {
		set[k] = !v.IsNull()
	}
	for _, k := range sortedKeys(set) {
		if _, ok := allowed[k]; set[k] && !ok {
			diags.AddAttributeError(p.AtName(k), "Option not supported by this merge policy",
				fmt.Sprintf("`%s` is not an option of the `%s` merge policy", k, typ))
		}
	}
	if !mp.MergeFactor.IsNull() && !mp.MaxMergeFactor.IsNull() && mp.MergeFactor.ValueInt64() > mp.MaxMergeFactor.ValueInt64() {
		diags.AddAttributeError(p.AtName("merge_factor"), "Invalid merge policy", "merge_factor must be lower or equal to max_merge_factor")
	}
	return diags
}

func canonicalSet(ctx context.Context, l types.List, diags *diag.Diagnostics) map[string]bool {
	out := map[string]bool{}
	var elems []normalizedValue
	diags.Append(l.ElementsAs(ctx, &elems, false)...)
	for _, e := range elems {
		if e.IsUnknown() || e.IsNull() {
			continue
		}
		s, err := e.normalized()
		if err != nil {
			s = e.ValueString()
		}
		out[s] = true
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
