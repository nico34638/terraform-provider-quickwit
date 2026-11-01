resource "quickwit_source_pubsub" "this" {
  index_id      = quickwit_index.logs.index_id
  source_id     = "pubsub-source"
  project_id    = "my-project"
  subscription  = "quickwit-logs"
  num_pipelines = 2
}
