data "quickwit_index" "logs" {
  index_id = "logs"
}

# Reuse the mapping of an existing index, e.g. for a copy with another retention.
resource "quickwit_index" "logs_archive" {
  index_id = "logs-archive"

  doc_mapping = {
    field_mappings  = data.quickwit_index.logs.doc_mapping.field_mappings
    timestamp_field = data.quickwit_index.logs.doc_mapping.timestamp_field
    tokenizers      = data.quickwit_index.logs.doc_mapping.tokenizers
  }

  retention = {
    period = "1 year"
  }
}

output "logs_sources" {
  value = [for s in data.quickwit_index.logs.sources : s.source_id if !startswith(s.source_id, "_")]
}
