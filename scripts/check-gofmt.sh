#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
set -euo pipefail
# Use the current checkout; NUL-delimited names include newly staged source and
# cannot confuse spaces/newlines/options with additional files.
cd "$(git rev-parse --show-toplevel)"
mapfile -d '' -t files < <(git ls-files -z -- '*.go')
if [[ ${#files[@]} == 0 ]]; then echo 'No tracked Go source found' >&2; exit 1; fi
for i in "${!files[@]}"; do files[$i]="./${files[$i]}"; done
unformatted=$(gofmt -l "${files[@]}")
if [[ -n $unformatted ]]; then
  echo 'Go source needs gofmt:' >&2
  printf '%s\n' "$unformatted" >&2
  exit 1
fi
echo 'Tracked Go source formatting verified'
