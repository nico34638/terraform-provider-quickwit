# End-to-end configuration driven by e2e/run.sh. Every object is named after
# var.prefix so that it never collides with existing indexes.
#
# phase = 1: create; phase = 2: in-place updates (new field, settings, sources).

terraform {
  required_providers {
    quickwit = {
      source = "nico34638/quickwit"
    }
  }
}

variable "endpoint" {
  type = string
}

variable "headers" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "prefix" {
  type = string
}

variable "phase" {
  type    = number
  default = 1
}

# Sources are created disabled unless asked: they would otherwise start
# indexing pipelines on the cluster.
variable "sources_enabled" {
  type    = bool
  default = false
}

variable "kafka_bootstrap" {
  type    = string
  default = ""
}

variable "kafka_topic" {
  type    = string
  default = ""
}

variable "sqs_queue_url" {
  type    = string
  default = ""
}

variable "kinesis_stream" {
  type    = string
  default = ""
}

variable "kinesis_region" {
  type    = string
  default = ""
}

variable "kinesis_endpoint" {
  type    = string
  default = ""
}

locals {
  p2 = var.phase >= 2
}

provider "quickwit" {
  endpoint = var.endpoint
  headers  = var.headers
  timeout  = "60s"
}

# --- Field mappings: one data source per type -------------------------------

data "quickwit_field_datetime" "timestamp" {
  name           = "timestamp"
  fast           = true
  fast_precision = "milliseconds"
  input_formats  = ["rfc3339", "unix_timestamp"]
}

data "quickwit_field_text" "severity" {
  name            = "severity_text"
  tokenizer       = "raw"
  fast_normalizer = "lowercase"
}

data "quickwit_field_text" "body" {
  name       = "body"
  tokenizer  = "trigram"
  record     = "position"
  fieldnorms = true
}

data "quickwit_field_text" "service_name" {
  name      = "service_name"
  tokenizer = "raw"
  fast      = true
}

data "quickwit_field_object" "resource" {
  name           = "resource"
  field_mappings = [data.quickwit_field_text.service_name.json]
}

data "quickwit_field_numeric" "status" {
  name = "status"
  type = "u64"
  fast = true
}

data "quickwit_field_numeric" "tenant" {
  name = "tenant_id"
  type = "i64"
}

data "quickwit_field_numeric" "latencies" {
  name          = "latencies_ms"
  type          = "f64"
  array         = true
  output_format = "string"
}

data "quickwit_field_bool" "error" {
  name = "is_error"
  fast = true
}

data "quickwit_field_ip" "client_ip" {
  name = "client_ip"
}

data "quickwit_field_bytes" "trace_id" {
  name          = "trace_id"
  input_format  = "hex"
  output_format = "hex"
}

data "quickwit_field_json" "attributes" {
  name = "attributes"
  fast = true
}

data "quickwit_field_concatenate" "everything" {
  name               = "everything"
  concatenate_fields = ["body", "severity_text"]
}

# Added in phase 2.
data "quickwit_field_text" "host" {
  name      = "host"
  tokenizer = "raw"
}

data "quickwit_tokenizer" "trigram" {
  name     = "trigram"
  type     = "ngram"
  min_gram = 3
  max_gram = 3
  filters  = ["lower_caser"]
}

# --- Indexes -----------------------------------------------------------------

resource "quickwit_index" "logs" {
  index_id = "${var.prefix}-logs"

  doc_mapping = {
    mode = "lenient"
    field_mappings = concat([
      data.quickwit_field_datetime.timestamp.json,
      data.quickwit_field_text.severity.json,
      data.quickwit_field_text.body.json,
      data.quickwit_field_object.resource.json,
      data.quickwit_field_numeric.status.json,
      data.quickwit_field_numeric.tenant.json,
      data.quickwit_field_numeric.latencies.json,
      data.quickwit_field_bool.error.json,
      data.quickwit_field_ip.client_ip.json,
      data.quickwit_field_bytes.trace_id.json,
      data.quickwit_field_json.attributes.json,
      data.quickwit_field_concatenate.everything.json,
    ], local.p2 ? [data.quickwit_field_text.host.json] : [])
    timestamp_field = "timestamp"
    tag_fields      = ["severity_text", "tenant_id"]
    tokenizers      = [data.quickwit_tokenizer.trigram.json]
  }

  indexing_settings = {
    commit_timeout_secs = local.p2 ? 15 : 30
    merge_policy = {
      type         = "stable_log"
      merge_factor = local.p2 ? 8 : 10
    }
  }

  search_settings = {
    default_search_fields = local.p2 ? ["body", "everything"] : ["body"]
  }

  retention = {
    period   = local.p2 ? "7 days" : "30 days"
    schedule = "daily"
  }
}

resource "quickwit_index" "minimal" {
  index_id = "${var.prefix}-minimal"

  doc_mapping = {
    store_source = local.p2
  }
}

# --- Sources -----------------------------------------------------------------

# Pub/Sub sources are saved without connectivity check, so this one works on
# any cluster. It stays disabled: the subscription does not exist.
resource "quickwit_source_pubsub" "this" {
  index_id      = quickwit_index.logs.index_id
  source_id     = "${var.prefix}-pubsub"
  subscription  = "${var.prefix}-${local.p2 ? "sub-v2" : "sub"}"
  project_id    = "e2e-project"
  num_pipelines = local.p2 ? 2 : 1
  enabled       = false

  transform = local.p2 ? null : {
    script = ".body = downcase(string!(.body))"
  }

  lifecycle {
    replace_triggered_by = [quickwit_index.logs]
  }
}

resource "quickwit_source_kafka" "this" {
  count = var.kafka_bootstrap != "" ? 1 : 0

  index_id      = quickwit_index.logs.index_id
  source_id     = "${var.prefix}-kafka"
  topic         = var.kafka_topic
  num_pipelines = local.p2 ? 2 : 1
  enabled       = var.sources_enabled
  client_params = { "bootstrap.servers" = var.kafka_bootstrap }

  lifecycle {
    replace_triggered_by = [quickwit_index.logs]
  }
}

resource "quickwit_source_sqs" "this" {
  count = var.sqs_queue_url != "" ? 1 : 0

  index_id     = quickwit_index.logs.index_id
  source_id    = "${var.prefix}-sqs"
  queue_url    = var.sqs_queue_url
  message_type = local.p2 ? "raw_uri" : "s3_notification"
  enabled      = var.sources_enabled

  lifecycle {
    replace_triggered_by = [quickwit_index.logs]
  }
}

resource "quickwit_source_kinesis" "this" {
  count = var.kinesis_stream != "" ? 1 : 0

  index_id             = quickwit_index.logs.index_id
  source_id            = "${var.prefix}-kinesis"
  stream_name          = var.kinesis_stream
  region               = var.kinesis_region != "" ? var.kinesis_region : null
  endpoint             = var.kinesis_endpoint != "" ? var.kinesis_endpoint : null
  enable_backfill_mode = local.p2
  enabled              = var.sources_enabled

  lifecycle {
    replace_triggered_by = [quickwit_index.logs]
  }
}

# --- Outputs read by run.sh --------------------------------------------------

output "logs_index_uid" {
  value = quickwit_index.logs.index_uid
}

output "logs_doc_mapping_uid" {
  value = quickwit_index.logs.doc_mapping.doc_mapping_uid
}

output "source_ids" {
  value = compact(concat(
    [quickwit_source_pubsub.this.source_id],
    quickwit_source_kafka.this[*].source_id,
    quickwit_source_sqs.this[*].source_id,
    quickwit_source_kinesis.this[*].source_id,
  ))
}
