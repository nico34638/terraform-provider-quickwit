data "quickwit_field_text" "message" {
  name       = "message"
  tokenizer  = "default"
  record     = "position"
  fieldnorms = true
}

data "quickwit_field_text" "host" {
  name            = "host"
  tokenizer       = "raw"
  fast_normalizer = "lowercase"
}
