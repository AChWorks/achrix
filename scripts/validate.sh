#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
set -euo pipefail

# Default remains the complete proof; focused development/CI may select Core.
scope=${1:-full}
if [[ $# -gt 1 || ( $scope != full && $scope != core ) ]]; then
  echo 'Usage: scripts/validate.sh [full|core]' >&2; exit 2
fi

repository=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repository"
export GOWORK=off
if [[ $(go env GOVERSION) != go1.27.1 ]]; then
  echo 'Go 1.27.1 is required for the documented proof' >&2; exit 1
fi
if [[ $(go env GOSUMDB) == off ]]; then
  echo 'Checksum verification must remain enabled' >&2; exit 1
fi
# Retain the already verified toolchain when the dependency module cache becomes
# cold; an older bootstrap Go need not download that identical SDK a second time.
export PATH="$(go env GOROOT)/bin:$PATH"
validation_root=$(mktemp -d /tmp/achrix-validation.XXXXXXXX)
started=0
cleanup() {
  result=$?
  if [[ $started == 1 ]]; then pg_ctl -D "$validation_root/database" -m fast -w stop >/dev/null || result=1; fi
  # Go's module cache is intentionally read-only. Use Go's supported cleanup on
  # this exact task-owned cache rather than trying to remove protected files.
  if [[ -d $validation_root/module-cache ]]; then
    GOMODCACHE="$validation_root/module-cache" go clean -modcache || result=1
  fi
  rm -rf -- "$validation_root" || result=1
  exit "$result"
}
trap cleanup EXIT
scripts/check-gofmt.sh
scripts/setup-quality-tools.sh "$validation_root/quality-tools"
quality_bin="$validation_root/quality-tools/bin"
"$quality_bin/staticcheck" ./...
"$quality_bin/govulncheck" -db https://vuln.go.dev -show version,verbose ./...
go vet ./...
go test -race -count=1 -timeout=30s ./...
go mod verify
if [[ $scope == core ]]; then
  echo 'Core validation verified; consumer/PostgreSQL proof not selected'
  exit 0
fi

if [[ -n ${ACHRIX_PG_BIN:-} ]]; then export PATH="$ACHRIX_PG_BIN:$PATH"; fi
for tool in initdb pg_ctl psql createdb pg_dump pg_restore; do
  command -v "$tool" >/dev/null
  if [[ $("$tool" --version | awk '{print $3}') != 18.6 ]]; then
    echo 'PostgreSQL 18.6 tools are required for the documented proof' >&2; exit 1
  fi
done
if [[ $(id -u) == 0 ]]; then echo 'Run validation as a non-root user' >&2; exit 1; fi

mkdir "$validation_root/socket"
initdb -D "$validation_root/database" -A trust --no-locale -E UTF8 >/dev/null
# A mode-0700 task-owned directory and Unix socket only; no host DB/service/port is used.
pg_ctl -D "$validation_root/database" -l "$validation_root/postgres.log" \
  -o "-k $validation_root/socket -h ''" -w start >/dev/null
started=1
export PGHOST="$validation_root/socket" PGPORT=5432 PGUSER
PGUSER=$(id -un)
createdb -T template0 achrix_test_notes
createdb -T template0 achrix_test_restore
createdb -T template0 -E LATIN1 achrix_test_latin1
createdb -T template0 -E SQL_ASCII achrix_test_sqlascii

# Copy consumer-owned source only. Foundation source is downloaded as a pinned
# normal Go module into a cold module cache; no replacement/workspace is used.
cp -R fixtures/notes "$validation_root/consumer"
export GOMODCACHE="$validation_root/module-cache"
cd "$validation_root/consumer"
go mod download
go mod verify
go list -m -json github.com/AChWorks/achrix > "$validation_root/foundation.json"
python3 - "$validation_root/foundation.json" <<'PY'
import json,sys,os
m=json.load(open(sys.argv[1]))
assert m['Path']=='github.com/AChWorks/achrix'
assert m.get('Version','').startswith('v0.') and 'Replace' not in m
assert m['Dir'].startswith(os.environ['GOMODCACHE']+os.sep)
assert m.get('Sum') and m.get('GoModSum')
print('Verified isolated pinned Foundation:',m['Version'])
PY
if go mod edit -json | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin).get("Replace") else 1)'; then
  echo 'Local dependency replacements are prohibited in this proof' >&2; exit 1
fi
"$quality_bin/staticcheck" ./...
"$quality_bin/govulncheck" -db https://vuln.go.dev -show version,verbose ./...
go vet ./...
export NOTES_TEST_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_test_notes sslmode=disable"
export NOTES_TEST_LATIN1_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_test_latin1 sslmode=disable"
export NOTES_TEST_SQL_ASCII_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_test_sqlascii sslmode=disable"
go test -race -count=1 -timeout=60s ./...

# Native trusted logical backup, no compression required. It covers this fixture's
# database/schema/ledger/data only; tokens/config/files/external effects are excluded.
pg_dump -Fc -Z0 --no-owner --no-acl -f "$validation_root/notes.dump" achrix_test_notes
(cd "$validation_root" && sha256sum notes.dump > notes.dump.sha256 && sha256sum -c notes.dump.sha256)
PGTZ=UTC psql -XAt -d achrix_test_notes -c 'SELECT row_to_json(e) FROM notes.entries e ORDER BY id' > "$validation_root/source.jsonl"
pg_restore --exit-on-error --no-owner --no-acl -d achrix_test_restore "$validation_root/notes.dump"
PGTZ=UTC psql -XAt -d achrix_test_restore -c 'SELECT row_to_json(e) FROM notes.entries e ORDER BY id' > "$validation_root/restore.jsonl"
cmp "$validation_root/source.jsonl" "$validation_root/restore.jsonl"
export NOTES_TEST_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_test_restore sslmode=disable"
NOTES_RESTORE_VERIFY=1 go test -race -count=1 -timeout=30s -run '^TestTrustedRestore$' .

build_identity=$(git -C "$repository" rev-parse HEAD)
if [[ -n $(git -C "$repository" status --porcelain) ]]; then build_identity="$build_identity-dirty"; fi
go build -trimpath -ldflags "-X main.buildIdentity=$build_identity" -o "$validation_root/notes" ./cmd/notes
"$validation_root/notes" -mode identity > "$validation_root/identity.json"
python3 - "$validation_root/foundation.json" "$validation_root/identity.json" <<'PY'
import json,sys
m=json.load(open(sys.argv[1])); b=json.load(open(sys.argv[2]))
assert b['foundation']==m['Version']
assert b['module']=='0.1.0-fixture' and len(b['migrations']['001_notes'])==64
assert b['build']!='development'
print('Verified composed build:',json.dumps(b,sort_keys=True))
PY
sha256sum "$validation_root/notes"
echo 'Foundation, isolated consumer, PostgreSQL, migrations, authorization and database restore verified'
