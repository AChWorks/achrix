#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
# Build exact reviewed tools in a new task-owned directory, never a host GOBIN.
set -euo pipefail
if [[ $# != 1 || -z $1 || -e $1 || -L $1 ]]; then
  echo 'Usage: scripts/setup-quality-tools.sh /absolute/new/owned/directory' >&2; exit 2
fi
case "$1" in /*) ;; *) echo 'An absolute directory is required' >&2; exit 2;; esac
export GOWORK=off
if [[ $(go env GOVERSION) != go1.27.1 ]]; then
  echo 'Go 1.27.1 is required for quality tools' >&2; exit 1
fi
if [[ $(go env GOSUMDB) == off ]]; then
  echo 'Checksum verification must remain enabled' >&2; exit 1
fi
export PATH="$(go env GOROOT)/bin:$PATH"
mkdir "$1"
destination=$(cd "$1" && pwd)
export GOBIN="$destination/bin"
mkdir "$GOBIN"
cd "$destination"

verify_module() {
  local module=$1 version=$2 sum=$3 manifest_sum=$4
  go mod download -json "$module@$version" > module.json
  python3 - "$module" "$version" "$sum" "$manifest_sum" <<'PY'
import json, sys
with open("module.json") as source:
    module = json.load(source)
expected = dict(zip(("Path", "Version", "Sum", "GoModSum"), sys.argv[1:]))
if module.get("Error") or any(module.get(k) != v for k, v in expected.items()):
    sys.exit("Quality tool module identity/checksum mismatch")
print("Verified quality tool source:", module["Path"], module["Version"], module["Sum"])
PY
}

# Staticcheck 2026.2.1, official tag commit:
# https://github.com/dominikh/go-tools/commit/1285a6a5ec1e0ebb658f49e82b6c566a878cc3cb
verify_module honnef.co/go/tools v0.8.1 \
  'h1:+JKf3xJ1ni4CwrhVg4/pqsfPGP6vNAXcKbMXJodYx3w=' \
  'h1:XA+OnlRA9EDh/ukGvXMNSZNKGwFQJ+5dER0ioUkOxks='
go install honnef.co/go/tools/cmd/staticcheck@v0.8.1
# govulncheck v1.8.0, official tag commit:
# https://go.googlesource.com/vuln/+/709015412431dd2b5b28a53c06c70bc02d49074c
verify_module golang.org/x/vuln v1.8.0 \
  'h1:clG4qBU6zH5VKjti8n5j8BBuYzoSha392xXMkXS351U=' \
  'h1:Fzm4XK3Hbl1ZvZ7JpNTEWb7CJWOZ7m2LX0GLu4Fsrwo='
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
# Emit compiled identity without consulting the network vulnerability database.
go version -m "$GOBIN/staticcheck" "$GOBIN/govulncheck"
