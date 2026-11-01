data "quickwit_field_datetime" "timestamp" {
  name           = "timestamp"
  fast           = true
  fast_precision = "milliseconds"
}

data "quickwit_field_text" "service" {
  name      = "service"
  tokenizer = "raw"
  fast      = true
}

data "quickwit_field_text" "message" {
  name   = "message"
  record = "position"
}

resource "quickwit_index" "logs" {
  index_id = "logs"

  doc_mapping = {
    mode = "dynamic"
    field_mappings = [
      data.quickwit_field_datetime.timestamp.json,
      data.quickwit_field_text.service.json,
      data.quickwit_field_text.message.json,
    ]
    timestamp_field = "timestamp"
    tag_fields      = ["service"]
  }

  search_settings = {
    default_search_fields = ["message"]
  }

  retention = {
    period = "30 days"
  }
}
