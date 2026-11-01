package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/nico34638/terraform-provider-quickwit/internal/client"
)

// Kafka, SQS and Kinesis sources need the `sources` profile of
// docker-compose.yml: Quickwit checks the connectivity of a source when it is
// created or updated. Names below are created by that profile.
const (
	testKafkaBootstrap = "kafka:29092"
	testSQSQueueURL    = "http://localstack:4566/000000000000/tf-acc"
	testKinesisURL     = "http://localstack:4566"
)

func testAccSourcesPreCheck(t *testing.T) {
	t.Helper()
	testAccPreCheck(t)
	if os.Getenv("QUICKWIT_TEST_SOURCES") == "" {
		t.Skip("set QUICKWIT_TEST_SOURCES=1 and run `docker compose --profile sources up -d`")
	}
}

func sourceIndexConfig(id string) string {
	return `
resource "quickwit_index" "test" {
  index_id    = "` + id + `"
  doc_mapping = {}
}
`
}

func TestAccSourcePubsub(t *testing.T) {
	id := randomIndexID()
	cfg := func(body string) string {
		return sourceIndexConfig(id) + `
resource "quickwit_source_pubsub" "test" {
  index_id  = quickwit_index.test.index_id
` + body + `
}`
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: cfg(`
  source_id     = "pubsub-src"
  subscription  = "my-sub"
  project_id    = "my-project"
  num_pipelines = 2
  transform     = { script = ".message = downcase(string!(.message))" }
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_source_pubsub.test", "id", id+":pubsub-src"),
					resource.TestCheckResourceAttr("quickwit_source_pubsub.test", "enabled", "true"),
					resource.TestCheckResourceAttr("quickwit_source_pubsub.test", "input_format", "json"),
					resource.TestCheckResourceAttr("quickwit_source_pubsub.test", "transform.timezone", "UTC"),
					resource.TestCheckNoResourceAttr("quickwit_source_pubsub.test", "credentials_file"),
					checkSourceAPI(id, "pubsub-src", func(s *client.Source) error {
						if s.NumPipelines != 2 || s.Transform == nil {
							return fmt.Errorf("unexpected source %+v", s)
						}
						return nil
					}),
				),
			},
			{
				Config: cfg(`
  source_id             = "pubsub-src"
  subscription          = "other-sub"
  enabled               = false
  max_messages_per_pull = 100
  input_format          = "plain_text"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_source_pubsub.test", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_source_pubsub.test", "enabled", "false"),
					resource.TestCheckNoResourceAttr("quickwit_source_pubsub.test", "transform.script"),
					resource.TestCheckNoResourceAttr("quickwit_source_pubsub.test", "project_id"),
					checkSourceAPI(id, "pubsub-src", func(s *client.Source) error {
						if s.Enabled || s.NumPipelines != 1 || s.InputFormat != "plain_text" {
							return fmt.Errorf("unexpected source %+v", s)
						}
						return nil
					}),
				),
			},
			{ResourceName: "quickwit_source_pubsub.test", ImportState: true, ImportStateId: id + ":pubsub-src", ImportStateVerify: true},
			// Deleted outside Terraform: planned for creation again.
			{
				PreConfig: func() { deleteSource(t, id, "pubsub-src") },
				Config: cfg(`
  source_id             = "pubsub-src"
  subscription          = "other-sub"
  enabled               = false
  max_messages_per_pull = 100
  input_format          = "plain_text"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_source_pubsub.test", plancheck.ResourceActionCreate),
				}},
			},
			{
				Config: cfg(`
  source_id    = "pubsub-renamed"
  subscription = "other-sub"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_source_pubsub.test", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: checkSourceGone(id, "pubsub-src"),
			},
		},
	})
}

func TestAccSourceKafka(t *testing.T) {
	id := randomIndexID()
	cfg := func(topic, extra string) string {
		return sourceIndexConfig(id) + `
resource "quickwit_source_kafka" "test" {
  index_id      = quickwit_index.test.index_id
  source_id     = "kafka-src"
  topic         = "` + topic + `"
  client_params = { "bootstrap.servers" = "` + testKafkaBootstrap + `" }
` + extra + `
}`
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccSourcesPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: cfg("tf-acc-1", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_source_kafka.test", "num_pipelines", "1"),
					resource.TestCheckResourceAttr("quickwit_source_kafka.test", "client_params.bootstrap.servers", testKafkaBootstrap),
				),
			},
			{
				Config: cfg("tf-acc-1", `
  num_pipelines        = 2
  client_log_level     = "warn"
  enable_backfill_mode = true
  enabled              = false
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_source_kafka.test", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("quickwit_source_kafka.test", "num_pipelines", "2"),
					resource.TestCheckResourceAttr("quickwit_source_kafka.test", "enable_backfill_mode", "true"),
				),
			},
			{ResourceName: "quickwit_source_kafka.test", ImportState: true, ImportStateId: id + ":kafka-src", ImportStateVerify: true},
			// The topic is immutable.
			{
				Config: cfg("tf-acc-2", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_source_kafka.test", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.TestCheckResourceAttr("quickwit_source_kafka.test", "topic", "tf-acc-2"),
			},
			// Connectivity is checked by Quickwit.
			{
				Config:      cfg("does-not-exist", ""),
				ExpectError: regexp.MustCompile("topic `does-not-exist` does not exist|does-not-exist"),
			},
		},
	})
}

func TestAccSourceSQS(t *testing.T) {
	id := randomIndexID()
	cfg := func(extra string) string {
		return sourceIndexConfig(id) + `
resource "quickwit_source_sqs" "test" {
  index_id  = quickwit_index.test.index_id
  source_id = "sqs-src"
  queue_url = "` + testSQSQueueURL + `"
` + extra + `
}`
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccSourcesPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			{
				Config: cfg(`message_type = "s3_notification"`),
				Check:  resource.TestCheckResourceAttr("quickwit_source_sqs.test", "deduplication_window_duration_secs", "3600"),
			},
			{
				Config: cfg(`
  message_type                      = "raw_uri"
  num_pipelines                     = 2
  deduplication_window_max_messages = 10
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_source_sqs.test", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("quickwit_source_sqs.test", "deduplication_window_max_messages", "10"),
			},
			{ResourceName: "quickwit_source_sqs.test", ImportState: true, ImportStateId: id + ":sqs-src", ImportStateVerify: true},
		},
	})
}

func TestAccSourceKinesis(t *testing.T) {
	id := randomIndexID()
	cfg := func(extra string) string {
		return sourceIndexConfig(id) + `
resource "quickwit_source_kinesis" "test" {
  index_id    = quickwit_index.test.index_id
  source_id   = "kinesis-src"
  stream_name = "tf-acc"
  endpoint    = "` + testKinesisURL + `"
` + extra + `
}`
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccSourcesPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             checkIndexDestroyed(id),
		Steps: []resource.TestStep{
			{Config: cfg(""), Check: resource.TestCheckNoResourceAttr("quickwit_source_kinesis.test", "region")},
			{
				Config: cfg(`
  enable_backfill_mode = true
  transform            = { script = ".x = 1", timezone = "Europe/Paris" }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_source_kinesis.test", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("quickwit_source_kinesis.test", "transform.timezone", "Europe/Paris"),
			},
			{ResourceName: "quickwit_source_kinesis.test", ImportState: true, ImportStateId: id + ":kinesis-src", ImportStateVerify: true},
			{
				Config: cfg(`enable_backfill_mode = true`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("quickwit_source_kinesis.test", plancheck.ResourceActionUpdate),
				}},
			},
		},
	})
}

func TestAccSource_planValidation(t *testing.T) {
	src := func(typ, body string) string {
		return `
resource "quickwit_source_` + typ + `" "test" {
  index_id = "some-index"
` + body + `
}`
	}
	cases := map[string]struct{ config, err string }{
		"reserved source id": {src("pubsub", `
  source_id    = "_ingest-source"
  subscription = "s"`), `_ingest-source`},
		"invalid source id": {src("pubsub", `
  source_id    = "ab"
  subscription = "s"`), `must start with a letter`},
		"several pipelines on kinesis": {src("kinesis", `
  source_id     = "kin"
  stream_name   = "s"
  num_pipelines = 2`), `at most 1`},
		"region and endpoint": {src("kinesis", `
  source_id   = "kin"
  stream_name = "s"
  region      = "eu-west-1"
  endpoint    = "http://localhost:4566"`), `cannot be specified when`},
		"transform with otlp": {src("pubsub", `
  source_id    = "pubsub-src"
  subscription = "s"
  input_format = "otlp_logs_json"
  transform    = { script = ".x = 1" }`), `not supported for OTLP`},
		"unknown input format alias": {src("pubsub", `
  source_id    = "pubsub-src"
  subscription = "s"
  input_format = "plain"`), `value must be one of`},
		"pulsar address": {src("pulsar", `
  source_id = "pulsar-src"
  topics    = ["t"]
  address   = "http://broker:6650"`), `pulsar://host:port`},
		"pulsar token and oauth2": {src("pulsar", `
  source_id      = "pulsar-src"
  topics         = ["t"]
  address        = "pulsar://broker:6650"
  authentication = {
    token  = "x"
    oauth2 = { issuer_url = "https://issuer", credentials_url = "file:///creds.json" }
  }`), `cannot be specified when`},
		"pulsar empty authentication": {src("pulsar", `
  source_id      = "pulsar-src"
  topics         = ["t"]
  address        = "pulsar://broker:6650"
  authentication = {}`), `Missing Pulsar authentication method`},
		"sqs message type": {src("sqs", `
  source_id    = "sqs-src"
  queue_url    = "http://localstack:4566/000000000000/q"
  message_type = "s3"`), `value must be one of`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviders,
				Steps:                    []resource.TestStep{{Config: tc.config, PlanOnly: true, ExpectError: regexp.MustCompile(tc.err)}},
			})
		})
	}
}

func testClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Endpoint: endpointFromEnv()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func deleteSource(t *testing.T, indexID, sourceID string) {
	if err := testClient(t).DeleteSource(context.Background(), indexID, sourceID); err != nil {
		t.Fatal(err)
	}
}

func checkSourceAPI(indexID, sourceID string, check func(*client.Source) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c, err := client.New(client.Config{Endpoint: endpointFromEnv()})
		if err != nil {
			return err
		}
		s, err := c.GetSource(context.Background(), indexID, sourceID)
		if err != nil {
			return err
		}
		return check(s)
	}
}

func checkSourceGone(indexID, sourceID string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c, err := client.New(client.Config{Endpoint: endpointFromEnv()})
		if err != nil {
			return err
		}
		_, err = c.GetSource(context.Background(), indexID, sourceID)
		if client.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("source %s:%s still exists", indexID, sourceID)
	}
}
