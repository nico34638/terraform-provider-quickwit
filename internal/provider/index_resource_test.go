package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/nico34638/terraform-provider-quickwit/internal/client"
)

func randomIndexID() string {
	return "tfacc-" + acctest.RandString(8)
}

// fieldsConfig declares one data source per field type, plus a tokenizer.
const fieldsConfig = `
data "quickwit_field_datetime" "ts" {
  name           = "timestamp"
  fast           = true
  fast_precision = "milliseconds"
  input_formats  = ["rfc3339", "unix_timestamp"]
}
data "quickwit_field_text" "service" {
  name      = "service"
  tokenizer = "raw"
  fast      = true
}
data "quickwit_field_text" "body" {
  name       = "body"
  tokenizer  = "ngram3"
  record     = "position"
  fieldnorms = true
}
data "quickwit_field_text" "lowercase" {
  name            = "host"
  fast_normalizer = "lowercase"
  stored          = false
}
data "quickwit_field_numeric" "status" {
  name = "status"
  type = "u64"
  fast = true
}
data "quickwit_field_numeric" "latencies" {
  name          = "latencies"
  type          = "f64"
  array         = true
  output_format = "string"
}
data "quickwit_field_numeric" "tenant" {
  name = "tenant_id"
  type = "i64"
}
data "quickwit_field_bool" "ok" {
  name = "ok"
}
data "quickwit_field_ip" "ip" {
  name = "client_ip"
  fast = true
}
data "quickwit_field_bytes" "payload" {
  name         = "payload"
  input_format = "hex"
}
data "quickwit_field_json" "attrs" {
  name        = "attributes"
  expand_dots = false
  fast        = true
}
data "quickwit_field_text" "svc_name" {
  name      = "name"
  tokenizer = "raw"
}
data "quickwit_field_object" "resource" {
  name           = "resource"
  field_mappings = [data.quickwit_field_text.svc_name.json]
}
data "quickwit_field_concatenate" "all" {
  name               = "all"
  concatenate_fields = ["service", "body"]
}
data "quickwit_tokenizer" "ngram3" {
  name      = "ngram3"
  type      = "ngram"
  min_gram  = 3
  max_gram  = 3
  filters   = ["lower_caser"]
}
`

const allFields = `[
      data.quickwit_field_datetime.ts.json,
      data.quickwit_field_text.service.json,
      data.quickwit_field_text.body.json,
      data.quickwit_field_text.lowercase.json,
      data.quickwit_field_numeric.status.json,
      data.quickwit_field_numeric.latencies.json,
      data.quickwit_field_numeric.tenant.json,
      data.quickwit_field_bool.ok.json,
      data.quickwit_field_ip.ip.json,
      data.quickwit_field_bytes.payload.json,
      data.quickwit_field_json.attrs.json,
      data.quickwit_field_object.resource.json,
      data.quickwit_field_concatenate.all.json,
    ]`

func indexConfig(id, extraFields, timestampField, extra string) string {
	fields := allFields
	if extraFields != "" {
		fields = strings.Replace(allFields, "    ]", "      "+extraFields+",\n    ]", 1)
	}
	return fieldsConfig + `
data "quickwit_field_text" "extra" {
  name = "extra"
}
data "quickwit_field_datetime" "ts2" {
  name = "timestamp2"
  fast = true
}

resource "quickwit_index" "test" {
  index_id = "` + id + `"
  doc_mapping = {
    mode            = "dynamic"
    field_mappings  = ` + fields + `
    timestamp_field = "` + timestampField + `"
    tag_fields      = ["service", "tenant_id"]
    tokenizers      = [data.quickwit_tokenizer.ngram3.json]
  }
` + extra + `
}
`
}

func TestAccIndex_lifecycle(t *testing.T) {
	id := randomIndexID()
	var uid, mappingUID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			// Create with every field type; the framework fails the step if
			// the follow-up plan is not empty (round-trip check).
			{
				Config: indexConfig(id, "", "timestamp", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_index.test", "id", id),
					resource.TestCheckResourceAttr("quickwit_index.test", "doc_mapping.field_mappings.#", "13"),
					resource.TestCheckResourceAttr("quickwit_index.test", "indexing_settings.heap_size", "2GB"),
					resource.TestCheckResourceAttr("quickwit_index.test", "indexing_settings.merge_policy.type", "stable_log"),
					resource.TestCheckNoResourceAttr("quickwit_index.test", "indexing_settings.merge_policy.merge_factor"),
					resource.TestCheckNoResourceAttr("quickwit_index.test", "doc_mapping.dynamic_mapping"),
					resource.TestCheckResourceAttrSet("quickwit_index.test", "index_uri"),
					storeAttr("quickwit_index.test", "index_uid", &uid),
					storeAttr("quickwit_index.test", "doc_mapping.doc_mapping_uid", &mappingUID),
				),
			},
			// Settings-only update: in place, doc_mapping_uid stays known and unchanged.
			{
				Config: indexConfig(id, "", "timestamp", `
  indexing_settings = {
    commit_timeout_secs = 10
    heap_size           = "1GiB"
    merge_policy = {
      type              = "limit_merge"
      max_merge_ops     = 3
      maturation_period = "48h"
    }
  }
  search_settings = { default_search_fields = ["body"] }
  retention = { period = "30 days" }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_index.test", plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue("quickwit_index.test", tfjsonPath("doc_mapping", "doc_mapping_uid"), knownString(&mappingUID)),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_index.test", "indexing_settings.heap_size", "1GiB"),
					resource.TestCheckResourceAttr("quickwit_index.test", "indexing_settings.merge_policy.maturation_period", "48h"),
					resource.TestCheckResourceAttr("quickwit_index.test", "retention.schedule", "hourly"),
					checkAttrEquals("quickwit_index.test", "index_uid", &uid),
				),
			},
			// Adding a field: in place, new doc_mapping_uid; removing retention.
			{
				Config: indexConfig(id, "data.quickwit_field_text.extra.json", "timestamp", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_index.test", plancheck.ResourceActionUpdate),
					plancheck.ExpectUnknownValue("quickwit_index.test", tfjsonPath("doc_mapping", "doc_mapping_uid")),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_index.test", "doc_mapping.field_mappings.#", "14"),
					resource.TestCheckNoResourceAttr("quickwit_index.test", "retention.period"),
					checkAttrEquals("quickwit_index.test", "index_uid", &uid),
					checkAttrDiffers("quickwit_index.test", "doc_mapping.doc_mapping_uid", &mappingUID),
				),
			},
			// Import.
			{
				ResourceName:      "quickwit_index.test",
				ImportState:       true,
				ImportStateId:     id,
				ImportStateVerify: true,
			},
			// Removing a tokenizer is refused at plan time.
			{
				Config:      strings.Replace(indexConfig(id, "data.quickwit_field_text.extra.json", "timestamp", ""), "tokenizers      = [data.quickwit_tokenizer.ngram3.json]", "tokenizers = []", 1),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`Tokenizers cannot be removed or changed`),
			},
			// Changing the timestamp field replaces the index.
			{
				Config: indexConfig(id, "data.quickwit_field_datetime.ts2.json", "timestamp2", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_index.test", plancheck.ResourceActionReplace),
				}},
				Check: checkAttrDiffers("quickwit_index.test", "index_uid", &uid),
			},
		},
	})
}

func TestAccIndex_planValidation(t *testing.T) {
	id := randomIndexID()
	cases := map[string]struct{ config, err string }{
		"missing timestamp field": {indexConfig(id, "", "nope", ""), `could not find timestamp field\s+` + "`nope`"},
		"timestamp not datetime":  {indexConfig(id, "", "service", ""), `should be a datetime field`},
		"timestamp not fast":      {strings.Replace(indexConfig(id, "", "timestamp", ""), "  fast           = true\n  fast_precision", "  fast_precision", 1), `should be a fast field`},
		"unknown tag field":       {strings.Replace(indexConfig(id, "", "timestamp", ""), `["service", "tenant_id"]`, `["nope"]`, 1), `unknown tag field`},
		"tag on tokenized text":   {strings.Replace(indexConfig(id, "", "timestamp", ""), `["service", "tenant_id"]`, `["body"]`, 1), `raw`},
		"retention without timestamp": {`
resource "quickwit_index" "test" {
  index_id    = "` + id + `"
  doc_mapping = {}
  retention   = { period = "1 day" }
}`, `Retention requires a timestamp field`},
		"invalid retention period": {indexConfig(id, "", "timestamp", `  retention = { period = "30 dayz" }`), `unknown unit "dayz"`},
		"5-field cron schedule": {indexConfig(id, "", "timestamp", `  retention = {
    period   = "1 day"
    schedule = "0 3 * * *"
  }`), `expected 6 or 7 fields`},
		"dynamic_mapping in strict mode": {`
resource "quickwit_index" "test" {
  index_id    = "` + id + `"
  doc_mapping = {
    mode            = "strict"
    dynamic_mapping = jsonencode({ stored = false })
  }
}`, `dynamic_mapping requires mode`},
		"invalid hand-written json": {`
resource "quickwit_index" "test" {
  index_id    = "` + id + `"
  doc_mapping = { field_mappings = [jsonencode({ name = "x", type = "array<bytes>", fast = true })] }
}`, `fast field is not allowed for array<bytes>`},
		"merge policy option of another type": {`
resource "quickwit_index" "test" {
  index_id          = "` + id + `"
  doc_mapping       = {}
  indexing_settings = { merge_policy = { type = "stable_log", max_merge_ops = 3 } }
}`, "`max_merge_ops` is not an option of the `stable_log` merge policy"},
		"empty concatenate": {`
data "quickwit_field_concatenate" "c" { name = "c" }`, `at least one\s+sub-field`},
		"empty object": {`
data "quickwit_field_object" "o" {
  name           = "o"
  field_mappings = []
}`, `at least one\s+field\s+mapping`},
		"array bytes fast": {`
data "quickwit_field_bytes" "b" {
  name  = "b"
  array = true
  fast  = true
}`, `fast field is not allowed for array<bytes>`},
		"relative index_uri": {`
resource "quickwit_index" "test" {
  index_id    = "` + id + `"
  index_uri   = "./data/` + id + `"
  doc_mapping = {}
}`, `relative path`},
		"bad index id": {`
resource "quickwit_index" "test" {
  index_id    = "1x"
  doc_mapping = {}
}`, `must start with a letter`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviders,
				Steps: []resource.TestStep{{
					Config: tc.config, PlanOnly: true, ExpectError: regexp.MustCompile(tc.err),
				}},
			})
		})
	}
}

func TestAccIndex_minimal(t *testing.T) {
	id := randomIndexID()
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: `
resource "quickwit_index" "test" {
  index_id    = "` + id + `"
  doc_mapping = {}
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_index.test", "doc_mapping.mode", "dynamic"),
					resource.TestCheckNoResourceAttr("quickwit_index.test", "doc_mapping.dynamic_mapping"),
					resource.TestCheckResourceAttr("quickwit_index.test", "ingest_settings.validate_docs", "true"),
				),
			},
			{
				Config: `
resource "quickwit_index" "test" {
  index_id    = "` + id + `"
  doc_mapping = {
    dynamic_mapping = jsonencode({ stored = false, tokenizer = "default" })
    store_source    = true
  }
  ingest_settings = { min_shards = 2 }
}`,
				Check: resource.TestCheckResourceAttr("quickwit_index.test", "ingest_settings.min_shards", "2"),
			},
			{
				ResourceName: "quickwit_index.test", ImportState: true, ImportStateId: id, ImportStateVerify: true,
				// Import yields Quickwit's canonical JSON, not the short hand-written form.
				ImportStateVerifyIgnore: []string{"doc_mapping.dynamic_mapping"},
			},
		},
	})
}

func checkIndexDestroyed(id string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c, err := client.New(client.Config{Endpoint: endpointFromEnv()})
		if err != nil {
			return err
		}
		_, err = c.GetIndex(context.Background(), id)
		if client.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("index %s still exists", id)
	}
}

// TestAccIndex_indexURI: Quickwit stores file URIs cleaned (no `..`, no
// trailing slash). The state keeps the configured spelling, an equivalent
// spelling plans nothing (in particular no replacement, which would delete
// the index), and a different URI still recreates the index.
func TestAccIndex_indexURI(t *testing.T) {
	id := randomIndexID()
	root := "file:///quickwit/qwdata/indexes/"
	config := func(uri string) string {
		return `
resource "quickwit_index" "test" {
  index_id    = "` + id + `"
  index_uri   = "` + uri + `"
  doc_mapping = {}
}`
	}
	var uid string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: config(root + "tmp/../" + id + "/"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_index.test", "index_uri", root+"tmp/../"+id+"/"),
					storeAttr("quickwit_index.test", "index_uid", &uid),
				),
			},
			{
				Config: config(root + id),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				}},
				Check: checkAttrEquals("quickwit_index.test", "index_uid", &uid),
			},
			{
				Config: config(root + id + "-moved"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_index.test", plancheck.ResourceActionReplace),
				}},
				Check: checkAttrDiffers("quickwit_index.test", "index_uid", &uid),
			},
		},
	})
}

// TestAccIndex_equivalentRewrites: rewriting values in an equivalent form
// plans nothing (no false diffs), and keeps doc_mapping_uid; a real change
// in the same configuration is still planned and applied.
func TestAccIndex_equivalentRewrites(t *testing.T) {
	id := randomIndexID()
	type values struct{ heap, maturation, dynamic, field, tokenizer string }
	config := func(v values) string {
		return `
resource "quickwit_index" "test" {
  index_id = "` + id + `"
  doc_mapping = {
    dynamic_mapping = ` + v.dynamic + `
    field_mappings  = [` + v.field + `]
    tokenizers      = [` + v.tokenizer + `]
  }
  indexing_settings = {
    heap_size    = "` + v.heap + `"
    merge_policy = { maturation_period = "` + v.maturation + `" }
  }
}`
	}
	original := values{
		heap:       "1GB",
		maturation: "3days",
		dynamic:    `jsonencode({ stored = false })`,
		field:      `jsonencode({ name = "message", type = "text" })`,
		tokenizer:  `jsonencode({ name = "trigram", type = "ngram", min_gram = 3, max_gram = 3 })`,
	}
	equivalent := values{
		heap:       "1000MB",
		maturation: "72h",
		dynamic:    `jsonencode({ indexed = true, stored = false })`,
		field:      `jsonencode({ type = "text", name = "message", indexed = true, stored = true, fast = false })`,
		tokenizer:  `jsonencode({ max_gram = 3, min_gram = 3, name = "trigram", type = "ngram", prefix_only = false, filters = [] })`,
	}
	changed := equivalent
	changed.heap = "2GB"

	var mappingUID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: config(original),
				Check:  storeAttr("quickwit_index.test", "doc_mapping.doc_mapping_uid", &mappingUID),
			},
			{
				Config: config(equivalent),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					// The state keeps the spelling it had.
					resource.TestCheckResourceAttr("quickwit_index.test", "indexing_settings.heap_size", "1GB"),
					resource.TestCheckResourceAttr("quickwit_index.test", "indexing_settings.merge_policy.maturation_period", "3days"),
					checkAttrEquals("quickwit_index.test", "doc_mapping.doc_mapping_uid", &mappingUID),
				),
			},
			{
				Config: config(changed),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_index.test", plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue("quickwit_index.test", tfjsonPath("doc_mapping", "doc_mapping_uid"), knownString(&mappingUID)),
				}},
				Check: resource.TestCheckResourceAttr("quickwit_index.test", "indexing_settings.heap_size", "2GB"),
			},
		},
	})
}
