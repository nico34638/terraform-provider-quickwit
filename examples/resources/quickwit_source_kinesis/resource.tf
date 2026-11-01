resource "quickwit_source_kinesis" "this" {
  index_id    = quickwit_index.logs.index_id
  source_id   = "kinesis-source"
  stream_name = "logs"
  region      = "eu-west-1"
}
