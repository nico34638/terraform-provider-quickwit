# Index the files announced by S3 event notifications.
resource "quickwit_source_sqs" "this" {
  index_id     = quickwit_index.logs.index_id
  source_id    = "sqs-source"
  queue_url    = "https://sqs.eu-west-1.amazonaws.com/123456789012/quickwit-logs"
  message_type = "s3_notification"
}
