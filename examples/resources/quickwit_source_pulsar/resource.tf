resource "quickwit_source_pulsar" "this" {
  index_id  = quickwit_index.logs.index_id
  source_id = "pulsar-source"
  topics    = ["persistent://public/default/logs"]
  address   = "pulsar+ssl://pulsar.example.com:6651"

  authentication = {
    token = var.pulsar_token
  }
}
