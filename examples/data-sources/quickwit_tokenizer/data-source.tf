data "quickwit_tokenizer" "trigram" {
  name     = "trigram"
  type     = "ngram"
  min_gram = 3
  max_gram = 3
  filters  = ["lower_caser"]
}

data "quickwit_field_text" "title" {
  name      = "title"
  tokenizer = data.quickwit_tokenizer.trigram.name
}

# Pass the tokenizer JSON to quickwit_index.doc_mapping.tokenizers.
