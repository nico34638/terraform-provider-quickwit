package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccIndexDataSource(t *testing.T) {
	id := randomIndexID()
	const ds = "data.quickwit_index.test"
	const res = "quickwit_index.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: indexConfig(id, "", "timestamp", `
  retention = { period = "30 days" }
`) + `
resource "quickwit_source_pubsub" "test" {
  index_id     = quickwit_index.test.index_id
  source_id    = "pubsub-src"
  subscription = "sub"
  enabled      = false
}

data "quickwit_index" "test" {
  index_id   = quickwit_index.test.index_id
  depends_on = [quickwit_source_pubsub.test]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					// Same values as the resource…
					resource.TestCheckResourceAttrPair(ds, "index_uid", res, "index_uid"),
					resource.TestCheckResourceAttrPair(ds, "index_uri", res, "index_uri"),
					resource.TestCheckResourceAttrPair(ds, "doc_mapping.doc_mapping_uid", res, "doc_mapping.doc_mapping_uid"),
					resource.TestCheckResourceAttrPair(ds, "doc_mapping.field_mappings.#", res, "doc_mapping.field_mappings.#"),
					resource.TestCheckResourceAttrPair(ds, "doc_mapping.field_mappings.0", res, "doc_mapping.field_mappings.0"),
					resource.TestCheckResourceAttrPair(ds, "doc_mapping.tokenizers.0", res, "doc_mapping.tokenizers.0"),
					resource.TestCheckResourceAttrPair(ds, "doc_mapping.timestamp_field", res, "doc_mapping.timestamp_field"),
					resource.TestCheckResourceAttr(ds, "doc_mapping.tag_fields.#", "2"),
					resource.TestCheckResourceAttr(ds, "retention.period", "30 days"),
					resource.TestCheckResourceAttr(ds, "retention.schedule", "hourly"),
					// …plus the server defaults the resource leaves null.
					resource.TestCheckResourceAttr(ds, "doc_mapping.dynamic_mapping", `{"expand_dots":true,"fast":{"normalizer":"raw"},"indexed":true,"record":"basic","stored":true,"tokenizer":"raw"}`),
					resource.TestCheckResourceAttr(ds, "indexing_settings.merge_policy.merge_factor", "10"),
					resource.TestCheckResourceAttr(ds, "indexing_settings.merge_policy.maturation_period", "2days"),
					resource.TestCheckResourceAttr(ds, "indexing_settings.heap_size", "2GB"),
					resource.TestCheckResourceAttrSet(ds, "create_timestamp"),
					// Sources, sorted by id: the 3 built-in ones and ours.
					resource.TestCheckResourceAttr(ds, "sources.#", "4"),
					resource.TestCheckResourceAttr(ds, "sources.3.source_id", "pubsub-src"),
					resource.TestCheckResourceAttr(ds, "sources.3.source_type", "pubsub"),
					resource.TestCheckResourceAttr(ds, "sources.3.enabled", "false"),
				),
			},
			{
				Config:      `data "quickwit_index" "missing" { index_id = "` + id + `-missing" }`,
				ExpectError: regexp.MustCompile(`not found`),
			},
		},
	})
}

func TestIndexDataSourceSchema(t *testing.T) {
	ctx := context.Background()
	resp := &datasource.SchemaResponse{}
	NewIndexDataSource().Schema(ctx, datasource.SchemaRequest{}, resp)
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("schema: %v", diags)
	}
}
