data "quickwit_field_text" "service_name" {
  name      = "name"
  tokenizer = "raw"
}

data "quickwit_field_numeric" "service_port" {
  name = "port"
  type = "u64"
}

# Objects nest the JSON of other field data sources (recursively).
data "quickwit_field_object" "service" {
  name = "service"
  field_mappings = [
    data.quickwit_field_text.service_name.json,
    data.quickwit_field_numeric.service_port.json,
  ]
}
