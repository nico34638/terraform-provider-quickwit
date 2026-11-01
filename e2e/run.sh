#!/usr/bin/env bash
# End-to-end test of the provider against a real Quickwit cluster.
#
# Builds the provider, applies e2e/main.tf with OpenTofu (or Terraform), then
# checks each step through the Quickwit API:
#   1. create  — indexes (every field type, tokenizer, settings) and sources
#   2. no-op   — a second plan must be empty (no spurious diffs)
#   3. update  — new field, settings and source changes, applied in place
#   4. import  — the index and the Pub/Sub source are removed from the state,
#                imported back, and the plan must stay empty
#   5. destroy — everything is deleted (skipped with E2E_KEEP=1)
#
# Everything is named `<E2E_PREFIX>-…`; the script refuses to run if such an
# index already exists, and destroys what it created when a step fails.
#
# Usage:
#   QUICKWIT_ENDPOINT=https://quickwit.example.com e2e/run.sh
#
# Environment:
#   QUICKWIT_ENDPOINT    cluster URL, without /api/v1 (default http://localhost:7280)
#   E2E_HEADERS          JSON object of extra HTTP headers, e.g. '{"Authorization":"Bearer …"}'
#   E2E_PREFIX           name prefix (default tfe2e-<random>)
#   E2E_KEEP=1           keep the created objects and the work directory
#   E2E_SOURCES_ENABLED=1  create Kafka/SQS/Kinesis sources enabled (default: disabled)
#   E2E_KAFKA_BOOTSTRAP + E2E_KAFKA_TOPIC     also test a Kafka source
#   E2E_SQS_QUEUE_URL                         also test an SQS source
#   E2E_KINESIS_STREAM + E2E_KINESIS_REGION or E2E_KINESIS_ENDPOINT
#                                             also test a Kinesis source
#   TF_BIN               tofu or terraform (default: tofu if installed)
#
# The addresses of Kafka, SQS and Kinesis are the ones Quickwit sees: the
# cluster checks it can reach a source before saving it.

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Outside the dev shell, re-run inside it to get go, tofu, curl and jq.
if [[ -z ${E2E_IN_NIX:-} && -z ${TF_BIN:-} ]] && ! { command -v go && command -v jq && { command -v tofu || command -v terraform; }; } >/dev/null 2>&1; then
  if command -v nix >/dev/null; then
    echo "Tools missing: re-running inside \`nix develop\`…" >&2
    E2E_IN_NIX=1 exec nix develop "$ROOT" -c "$ROOT/e2e/run.sh" "$@"
  fi
fi

ENDPOINT=${QUICKWIT_ENDPOINT:-http://localhost:7280}
ENDPOINT=${ENDPOINT%/}
HEADERS=${E2E_HEADERS:-"{}"}
PREFIX=${E2E_PREFIX:-tfe2e-$(printf '%04x%04x' $RANDOM $RANDOM)}
KEEP=${E2E_KEEP:-0}
TF_BIN=${TF_BIN:-$(command -v tofu || command -v terraform || true)}

# --- output helpers ----------------------------------------------------------

if [[ -t 1 ]]; then
  BOLD=$'\e[1m' GREEN=$'\e[32m' RED=$'\e[31m' DIM=$'\e[2m' RESET=$'\e[0m'
else
  BOLD='' GREEN='' RED='' DIM='' RESET=''
fi
STEP=0
step() { STEP=$((STEP + 1)); printf '\n%s== %d. %s%s\n' "$BOLD" "$STEP" "$*" "$RESET"; }
ok()   { printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$*"; }
info() { printf '  %s%s%s\n' "$DIM" "$*" "$RESET"; }
die()  { printf '\n%s✗ %s%s\n' "$RED" "$*" "$RESET" >&2; exit 1; }

# --- prerequisites -----------------------------------------------------------

for bin in go curl jq; do
  command -v "$bin" >/dev/null || die "$bin is required (run inside \`nix develop\`)"
done
[[ -n $TF_BIN ]] || die "tofu or terraform is required: run inside \`nix develop\` (or set TF_BIN)"
jq -e 'type == "object"' <<<"$HEADERS" >/dev/null || die "E2E_HEADERS must be a JSON object"
[[ $PREFIX =~ ^[a-zA-Z][a-zA-Z0-9-]{2,40}$ ]] || die "E2E_PREFIX must start with a letter and only contain letters, digits and '-'"

CURL_HEADERS=()
while IFS= read -r h; do CURL_HEADERS+=(-H "$h"); done < <(jq -r 'to_entries[] | "\(.key): \(.value)"' <<<"$HEADERS")

# api METHOD PATH: prints the body, returns the HTTP status in $STATUS.
api() {
  local out
  out=$(curl -sS -X "$1" "${CURL_HEADERS[@]}" -w '\n%{http_code}' "$ENDPOINT/api/v1/$2") || die "cannot reach $ENDPOINT"
  STATUS=${out##*$'\n'}
  BODY=${out%$'\n'*}
}

expect() { # expect DESCRIPTION JQ_FILTER (applied to $BODY, must be true)
  if jq -e "$2" <<<"$BODY" >/dev/null; then ok "$1"; else die "$1: check \`$2\` failed on: $(head -c 2000 <<<"$BODY")"; fi
}

# --- work directory and Terraform wrapper ------------------------------------

WORK=$(mktemp -d "${TMPDIR:-/tmp}/quickwit-e2e.XXXXXX")
mkdir -p "$WORK/bin" "$WORK/tf"
cp "$ROOT/e2e/main.tf" "$WORK/tf/"
cat >"$WORK/tfrc" <<EOF
provider_installation {
  dev_overrides { "nico34638/quickwit" = "$WORK/bin" }
  direct {}
}
EOF
export TF_CLI_CONFIG_FILE=$WORK/tfrc TF_IN_AUTOMATION=1
export TF_VAR_endpoint=$ENDPOINT TF_VAR_headers=$HEADERS TF_VAR_prefix=$PREFIX
export TF_VAR_sources_enabled=$([[ ${E2E_SOURCES_ENABLED:-0} == 1 ]] && echo true || echo false)
export TF_VAR_kafka_bootstrap=${E2E_KAFKA_BOOTSTRAP:-} TF_VAR_kafka_topic=${E2E_KAFKA_TOPIC:-}
export TF_VAR_sqs_queue_url=${E2E_SQS_QUEUE_URL:-}
export TF_VAR_kinesis_stream=${E2E_KINESIS_STREAM:-} TF_VAR_kinesis_region=${E2E_KINESIS_REGION:-}
export TF_VAR_kinesis_endpoint=${E2E_KINESIS_ENDPOINT:-}
[[ -z ${E2E_KAFKA_BOOTSTRAP:-} || -n ${E2E_KAFKA_TOPIC:-} ]] || die "E2E_KAFKA_TOPIC is required with E2E_KAFKA_BOOTSTRAP"

# Flags must come before positional arguments.
tf() { (cd "$WORK/tf" && "$TF_BIN" "$@") >>"$WORK/tf.log" 2>&1 || { tail -40 "$WORK/tf.log" >&2; die "$TF_BIN $1 failed (full log: $WORK/tf.log)"; }; }

# plan_empty PHASE: a plan must report no change.
plan_empty() {
  local rc=0
  (cd "$WORK/tf" && "$TF_BIN" plan -no-color -input=false -detailed-exitcode -var "phase=$1") >"$WORK/plan.log" 2>&1 || rc=$?
  case $rc in
    0) ok "plan is empty" ;;
    2) cat "$WORK/plan.log" >&2; die "unexpected diff after apply" ;;
    *) cat "$WORK/plan.log" >&2; die "$TF_BIN plan failed" ;;
  esac
}

output() { (cd "$WORK/tf" && "$TF_BIN" output -raw "$1"); }

CREATED=0
cleanup() {
  local rc=$?
  if [[ $CREATED == 1 && $KEEP != 1 && $rc != 0 ]]; then
    printf '\n%sCleaning up after failure…%s\n' "$BOLD" "$RESET"
    (cd "$WORK/tf" && "$TF_BIN" destroy -auto-approve -no-color -input=false) >>"$WORK/tf.log" 2>&1 \
      || printf '%sDestroy failed: delete the `%s-*` indexes by hand (log: %s)%s\n' "$RED" "$PREFIX" "$WORK/tf.log" "$RESET"
  fi
  if [[ $KEEP == 1 || $rc != 0 ]]; then
    printf '\nWork directory kept: %s\n' "$WORK"
  else
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT

# --- run ---------------------------------------------------------------------

LOGS=$PREFIX-logs MINIMAL=$PREFIX-minimal

step "Checks"
api GET version
[[ $STATUS == 200 ]] || die "GET $ENDPOINT/api/v1/version returned HTTP $STATUS: $BODY"
ok "Quickwit $(jq -r .build.version <<<"$BODY") at $ENDPOINT"
for idx in "$LOGS" "$MINIMAL"; do
  api GET "indexes/$idx"
  [[ $STATUS == 404 ]] || die "index $idx already exists (HTTP $STATUS): choose another E2E_PREFIX"
done
ok "prefix $PREFIX is free"
info "$TF_BIN: $("$TF_BIN" version | head -1)"
info "optional sources: kafka=${E2E_KAFKA_BOOTSTRAP:+on} sqs=${E2E_SQS_QUEUE_URL:+on} kinesis=${E2E_KINESIS_STREAM:+on} (enabled=$TF_VAR_sources_enabled)"

step "Build provider"
(cd "$ROOT" && go build -o "$WORK/bin/terraform-provider-quickwit" .)
ok "built into $WORK/bin"

step "Create (phase 1)"
CREATED=1
tf apply -no-color -auto-approve -input=false -var phase=1
ok "apply succeeded"
api GET "indexes/$LOGS"
expect "index $LOGS exists" '.index_config.index_id == "'"$LOGS"'"'
expect "12 field mappings" '.index_config.doc_mapping.field_mappings | length == 12'
expect "timestamp field and tags" '.index_config.doc_mapping.timestamp_field == "timestamp" and (.index_config.doc_mapping.tag_fields | sort == ["severity_text","tenant_id"])'
expect "trigram tokenizer" '.index_config.doc_mapping.tokenizers[0].name == "trigram"'
expect "retention 30 days" '.index_config.retention.period == "30 days"'
expect "commit timeout 30s" '.index_config.indexing_settings.commit_timeout_secs == 30'
SOURCES=$(cd "$WORK/tf" && "$TF_BIN" output -json source_ids | jq -r '.[]')
for sid in $SOURCES; do
  expect "source $sid exists" "[.sources[].source_id] | index(\"$sid\") != null"
done
expect "pubsub source has a transform" '.sources[] | select(.source_id == "'"$PREFIX"'-pubsub") | .transform.script != null'
api GET "indexes/$MINIMAL"
expect "index $MINIMAL exists in dynamic mode" '.index_config.doc_mapping.mode == "dynamic"'
INDEX_UID=$(output logs_index_uid)
MAPPING_UID=$(output logs_doc_mapping_uid)
info "index_uid=$INDEX_UID doc_mapping_uid=$MAPPING_UID"

step "Idempotence"
plan_empty 1

step "Update in place (phase 2)"
tf apply -no-color -auto-approve -input=false -var phase=2
ok "apply succeeded"
[[ $(output logs_index_uid) == "$INDEX_UID" ]] && ok "index was not recreated" || die "index_uid changed: the index was replaced"
[[ $(output logs_doc_mapping_uid) != "$MAPPING_UID" ]] && ok "doc_mapping_uid changed with the mapping" || die "doc_mapping_uid did not change"
api GET "indexes/$LOGS"
expect "host field added" '[.index_config.doc_mapping.field_mappings[].name] | index("host") != null'
expect "retention 7 days" '.index_config.retention.period == "7 days"'
expect "commit timeout 15s" '.index_config.indexing_settings.commit_timeout_secs == 15'
expect "merge factor 8" '.index_config.indexing_settings.merge_policy.merge_factor == 8'
expect "pubsub source updated" '.sources[] | select(.source_id == "'"$PREFIX"'-pubsub") | .num_pipelines == 2 and .params.subscription == "'"$PREFIX"'-sub-v2" and .transform == null'
api GET "indexes/$MINIMAL"
expect "store_source enabled on $MINIMAL" '.index_config.doc_mapping.store_source == true'
plan_empty 2

step "Import"
tf state rm quickwit_index.logs quickwit_source_pubsub.this
tf import -no-color -input=false -var phase=2 quickwit_index.logs "$LOGS"
tf import -no-color -input=false -var phase=2 quickwit_source_pubsub.this "$LOGS:$PREFIX-pubsub"
ok "imported $LOGS and $LOGS:$PREFIX-pubsub"
plan_empty 2

if [[ $KEEP == 1 ]]; then
  step "Keep"
  ok "objects kept (E2E_KEEP=1); destroy them with:"
  info "cd $WORK/tf && TF_CLI_CONFIG_FILE=$WORK/tfrc $TF_BIN destroy -var phase=2"
  info "(the TF_VAR_* variables of this run must be exported again)"
else
  step "Destroy"
  tf destroy -no-color -auto-approve -input=false -var phase=2
  CREATED=0
  for idx in "$LOGS" "$MINIMAL"; do
    api GET "indexes/$idx"
    [[ $STATUS == 404 ]] && ok "index $idx deleted" || die "index $idx still exists (HTTP $STATUS)"
  done
fi

printf '\n%s%sEnd-to-end test passed%s\n' "$BOLD" "$GREEN" "$RESET"
