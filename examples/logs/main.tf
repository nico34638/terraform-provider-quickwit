# Example: an OpenTelemetry-like logs index.
#   terraform apply
#   curl -s localhost:7280/api/v1/indexes/logs | jq .index_config

terraform {
  required_providers {
    quickwit = {
      source = "nico34638/quickwit"
    }
  }
}

provider "quickwit" {
  endpoint = var.endpoint
}

variable "endpoint" {
  type    = string
  default = "http://localhost:7280"
}

data "quickwit_field_datetime" "timestamp" {
  name           = "timestamp"
  fast           = true
  fast_precision = "milliseconds"
  input_formats  = ["rfc3339", "unix_timestamp"]
}

data "quickwit_field_text" "severity" {
  name      = "severity_text"
  tokenizer = "raw"
  fast      = true
}

data "quickwit_field_text" "body" {
  name       = "body"
  tokenizer  = "default"
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

data "quickwit_field_json" "attributes" {
  name = "attributes"
  fast = true
}

data "quickwit_field_ip" "client_ip" {
  name = "client_ip"
}

data "quickwit_field_numeric" "status" {
  name = "status"
  type = "u64"
  fast = true
}

resource "quickwit_index" "logs" {
  index_id = "logs"

  doc_mapping = {
    mode = "lenient"
    field_mappings = [
      data.quickwit_field_datetime.timestamp.json,
      data.quickwit_field_text.severity.json,
      data.quickwit_field_text.body.json,
      data.quickwit_field_object.resource.json,
      data.quickwit_field_json.attributes.json,
      data.quickwit_field_ip.client_ip.json,
      data.quickwit_field_numeric.status.json,
    ]
    timestamp_field = "timestamp"
    tag_fields      = ["severity_text", "resource.service_name"]
  }

  indexing_settings = {
    commit_timeout_secs = 10
  }

  search_settings = {
    default_search_fields = ["body"]
  }

  retention = {
    period   = "30 days"
    schedule = "daily"
  }
}

output "index_uid" {
  value = quickwit_index.logs.index_uid
}
