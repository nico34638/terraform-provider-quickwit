provider "quickwit" {
  endpoint = "http://localhost:7280"

  # Quickwit has no native authentication: authenticate against a proxy.
  # headers = { Authorization = "Bearer ${var.quickwit_token}" }

  timeout = "30s"
}
