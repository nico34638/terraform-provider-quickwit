#!/usr/bin/env bash
# Checks that commit subjects follow Conventional Commits
# (https://www.conventionalcommits.org), which goreleaser uses to group the
# release notes (.goreleaser.yml, changelog.groups).
#
#   <type>[(scope)][!]: <description>
#   types: feat fix perf refactor docs test build ci chore revert
#   "!" (or a "BREAKING CHANGE:" footer) marks a breaking change.
#
# Usage:
#   .githooks/check-commit-msg.sh --file .git/COMMIT_EDITMSG   # commit-msg hook
#   .githooks/check-commit-msg.sh <rev-range>                  # e.g. origin/main..HEAD

set -euo pipefail

pattern='^(feat|fix|perf|refactor|docs|test|build|ci|chore|revert)(\([a-z0-9_./-]+\))?!?: [^ ].*'
# Commits made by git itself.
exempt='^(Merge |Revert "|fixup! |squash! |amend! )'

check() {
  local sha=$1 subject=$2
  [[ $subject =~ $exempt || $subject =~ $pattern ]] && return 0
  printf '✗ %s"%s"\n' "${sha:+$sha }" "$subject" >&2
  return 1
}

usage_hint() {
  cat >&2 <<'MSG'
  Commit subjects must follow Conventional Commits:
    <type>[(scope)][!]: <description>
    types: feat fix perf refactor docs test build ci chore revert
  e.g. "feat(source): add the kafka source", "fix!: rename index_uri"
MSG
}

if [[ ${1:-} == --file ]]; then
  subject=$(grep -v '^#' "${2:?missing file}" | sed -n '/[^[:space:]]/{p;q;}')
  check "" "$subject" || { usage_hint; exit 1; }
  exit 0
fi

range=${1:?usage: $0 <rev-range> | --file <msg-file>}
failed=0
while IFS=$'\t' read -r sha subject; do
  check "$sha" "$subject" || failed=1
done < <(git log --no-merges --format='%h%x09%s' "$range")
[[ $failed == 0 ]] || { usage_hint; exit 1; }
echo "✓ commit messages follow Conventional Commits"
