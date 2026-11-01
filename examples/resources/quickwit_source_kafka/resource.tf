resource "quickwit_source_kafka" "this" {
  index_id      = quickwit_index.logs.index_id
  source_id     = "kafka-source"
  topic         = "logs"
  num_pipelines = 2

  client_params = {
    "bootstrap.servers" = "kafka-1:9092,kafka-2:9092"
    "security.protocol" = "SASL_SSL"
    "sasl.mechanism"    = "PLAIN"
    "sasl.username"     = "quickwit"
    "sasl.password"     = var.kafka_password
  }

  transform = {
    script = ".message = downcase(string!(.message))"
  }

  # The source is deleted with its index: recreate it when the index is replaced.
  lifecycle {
    replace_triggered_by = [quickwit_index.logs]
  }
}
