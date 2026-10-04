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
"$quality_bin/govulncheck" -test -db https://vuln.go.dev -show version,verbose ./...
go vet ./...
go mod verify
if [[ $scope == core ]]; then
  go test -race -count=1 -timeout=30s ./...
  echo 'Core validation verified; consumer/PostgreSQL proof not selected'
  exit 0
fi
go test -race -count=1 -timeout=30s .

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
createdb -T template0 achrix_identity_test
createdb -T template0 achrix_audit_test
createdb -T template0 achrix_identity_consumer
createdb -T template0 achrix_identity_restore
createdb -T template0 achrix_media_test
createdb -T template0 achrix_admin_test
createdb -T template0 achrix_media_consumer
createdb -T template0 achrix_media_restore
mkdir -m 0700 "$validation_root/media-module" "$validation_root/media-source" "$validation_root/media-restored"
createdb -T template0 -E LATIN1 achrix_test_latin1
createdb -T template0 -E SQL_ASCII achrix_test_sqlascii

# Module tests use distinct private databases: Go can run packages concurrently.
# Full scope runs their unit and real persisted tests once with explicit DSNs.
export ACHRIX_IDENTITY_TEST_DSN="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_identity_test sslmode=disable"
export ACHRIX_AUDIT_TEST_DSN="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_audit_test sslmode=disable"
export ACHRIX_MEDIA_TEST_DSN="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_media_test sslmode=disable"
export ACHRIX_ADMIN_TEST_DSN="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_admin_test sslmode=disable"
export ACHRIX_MEDIA_TEST_ROOT="$validation_root/media-module"
go test -race -count=1 -timeout=90s ./identity/... ./audit/... ./media/... ./admin/... ./multisite/...
unset ACHRIX_IDENTITY_TEST_DSN ACHRIX_AUDIT_TEST_DSN ACHRIX_MEDIA_TEST_DSN ACHRIX_MEDIA_TEST_ROOT ACHRIX_ADMIN_TEST_DSN

# Copy consumer-owned source only. Foundation source is downloaded as a pinned
# normal Go module into a cold module cache; no replacement/workspace is used.
cp -R fixtures/notes "$validation_root/consumer"
export GOMODCACHE="$validation_root/module-cache"
cd "$validation_root/consumer"
go mod download
go mod verify
go list -m -json github.com/AChWorks/achrix > "$validation_root/foundation.json"
python3 - "$validation_root/foundation.json" "$repository" <<'PY'
import json,sys,os,hashlib,pathlib
m=json.load(open(sys.argv[1]))
assert m['Path']=='github.com/AChWorks/achrix'
assert m.get('Version','').startswith('v0.') and 'Replace' not in m
assert m['Dir'].startswith(os.environ['GOMODCACHE']+os.sep)
assert m.get('Sum') and m.get('GoModSum')
print('Verified isolated pinned Foundation:',m['Version'])
root=pathlib.Path(sys.argv[2]); dependency=pathlib.Path(m['Dir'])
paths=[pathlib.Path('achrix.go')]
for package in ['identity','audit','media','admin','multisite']:
    assert (root/package).is_dir() and (dependency/package).is_dir()
    paths += sorted(p.relative_to(root) for p in (root/package).rglob('*')
                    if p.is_file() and p.suffix in ['.go','.sql','.js','.css','.html']
                    and not p.name.endswith('_test.go'))
digest=hashlib.sha256()
for relative in paths:
    source=(root/relative).read_bytes(); downloaded=(dependency/relative).read_bytes()
    assert source==downloaded, 'normal consumer source drift: '+str(relative)
    digest.update(str(relative).encode()+b'\0'+source)
print('Verified normal-module Core/Identity/Audit/Media/Admin/Multi-Site source SHA-256:',digest.hexdigest())
PY
if go mod edit -json | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin).get("Replace") else 1)'; then
  echo 'Local dependency replacements are prohibited in this proof' >&2; exit 1
fi
"$quality_bin/staticcheck" ./...
"$quality_bin/govulncheck" -test -db https://vuln.go.dev -show version,verbose ./...
go vet ./...
export NOTES_TEST_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_test_notes sslmode=disable"
export NOTES_TEST_LATIN1_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_test_latin1 sslmode=disable"
export NOTES_TEST_SQL_ASCII_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_test_sqlascii sslmode=disable"
export NOTES_IDENTITY_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_identity_consumer sslmode=disable"
export NOTES_MEDIA_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_media_consumer sslmode=disable"
export NOTES_MEDIA_STORAGE_ROOT="$validation_root/media-source"
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

# The consumer has stopped ingress/Modules before this quiescent native capture.
# Identity and atomic Audit are restored together; neither schema is separately
# restorable. Bearer/CSRF plaintext is never in either dataset. These protected
# dumps are private validation artifacts, not a production backup policy.
pg_dump -Fc -Z0 --no-owner --no-acl -f "$validation_root/identity-audit.dump" achrix_identity_consumer
(cd "$validation_root" && sha256sum identity-audit.dump > identity-audit.dump.sha256 && sha256sum -c identity-audit.dump.sha256)
pg_restore --exit-on-error --no-owner --no-acl -d achrix_identity_restore "$validation_root/identity-audit.dump"
for database in achrix_identity_consumer achrix_identity_restore; do
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(r) FROM audit.records r ORDER BY seq' > "$validation_root/$database-audit.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(a) FROM identity.accounts a ORDER BY id' > "$validation_root/$database-accounts.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(c) FROM identity.credentials c ORDER BY account_id' > "$validation_root/$database-credentials.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(s) FROM identity.sessions s ORDER BY token_hash' > "$validation_root/$database-sessions.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c "SELECT 'identity',version,checksum FROM identity.schema_migrations UNION ALL SELECT 'audit',version,checksum FROM audit.schema_migrations ORDER BY 1,2" > "$validation_root/$database-ledgers.txt"
done
for dataset in audit accounts credentials sessions; do
  cmp "$validation_root/achrix_identity_consumer-$dataset.jsonl" "$validation_root/achrix_identity_restore-$dataset.jsonl"
done
cmp "$validation_root/achrix_identity_consumer-ledgers.txt" "$validation_root/achrix_identity_restore-ledgers.txt"
export NOTES_IDENTITY_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_identity_restore sslmode=disable"
NOTES_IDENTITY_RESTORE_VERIFY=1 go test -race -count=1 -timeout=30s -run '^TestIdentityAuditTrustedRestore$' .

# Media consumer ingress and all Modules stopped before this coherent capture.
# This private profile restores the whole participating product database plus the
# exact private asset tree. No live snapshot/off-host/RPO/RTO guarantee is inferred.
pg_dump -Fc -Z0 --no-owner --no-acl -f "$validation_root/media.dump" achrix_media_consumer
python3 - "$validation_root/media-source" "$validation_root/media-files.json" <<'PYMEDIA'
import hashlib,json,os,pathlib,stat,sys
root=pathlib.Path(sys.argv[1])
assert stat.S_IMODE(root.stat().st_mode)==0o700
files={}
for path in sorted(root.rglob('*')):
    info=path.lstat()
    assert not stat.S_ISLNK(info.st_mode), 'private asset capture contains symlink'
    assert stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)
    assert stat.S_IMODE(info.st_mode)&0o077==0, 'private asset capture permissions widened'
    if path.is_file():
        files[str(path.relative_to(root))]=hashlib.sha256(path.read_bytes()).hexdigest()
assert len(files)==4, 'capture must retain PNG/JPEG, PDF and ZIP public-consumer assets'
pathlib.Path(sys.argv[2]).write_text(json.dumps(files,sort_keys=True))
print('Verified quiesced private asset capture:',len(files),'files')
PYMEDIA
tar -cf "$validation_root/media-assets.tar" -C "$validation_root/media-source" .
(cd "$validation_root" && sha256sum media.dump media-assets.tar media-files.json > media-capture.sha256 && sha256sum -c media-capture.sha256)
# The archive was generated from the private, non-symlink task tree above. Inspect
# its paths/types before extracting even this trusted validation artifact.
python3 - "$validation_root/media-assets.tar" <<'PYMEDIA'
import pathlib,sys,tarfile
with tarfile.open(sys.argv[1]) as archive:
    for entry in archive:
        path=pathlib.PurePosixPath(entry.name)
        assert not path.is_absolute() and '..' not in path.parts
        assert entry.isfile() or entry.isdir(), 'private asset archive contains unsafe entry'
PYMEDIA
pg_restore --exit-on-error --no-owner --no-acl -d achrix_media_restore "$validation_root/media.dump"
tar -xf "$validation_root/media-assets.tar" -C "$validation_root/media-restored"
python3 - "$validation_root/media-restored" "$validation_root/media-files.json" <<'PYMEDIA'
import hashlib,json,pathlib,stat,sys
root=pathlib.Path(sys.argv[1]); expected=json.loads(pathlib.Path(sys.argv[2]).read_text()); actual={}
assert stat.S_IMODE(root.stat().st_mode)==0o700
for path in sorted(root.rglob('*')):
    info=path.lstat()
    assert not stat.S_ISLNK(info.st_mode)
    assert stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)
    assert stat.S_IMODE(info.st_mode)&0o077==0
    if path.is_file(): actual[str(path.relative_to(root))]=hashlib.sha256(path.read_bytes()).hexdigest()
assert actual==expected, 'restored private asset identity/hash differs'
print('Verified private asset restore manifest:',len(actual),'files')
PYMEDIA
for database in achrix_media_consumer achrix_media_restore; do
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(a) FROM media.assets a ORDER BY id' > "$validation_root/$database-media.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(r) FROM audit.records r ORDER BY seq' > "$validation_root/$database-audit.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(a) FROM identity.accounts a ORDER BY id' > "$validation_root/$database-accounts.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(c) FROM identity.credentials c ORDER BY account_id' > "$validation_root/$database-credentials.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c 'SELECT row_to_json(s) FROM identity.sessions s ORDER BY token_hash' > "$validation_root/$database-sessions.jsonl"
  PGTZ=UTC psql -XAt -d "$database" -c "SELECT 'media',version,checksum FROM media.schema_migrations UNION ALL SELECT 'identity',version,checksum FROM identity.schema_migrations UNION ALL SELECT 'audit',version,checksum FROM audit.schema_migrations ORDER BY 1,2" > "$validation_root/$database-ledgers.txt"
done
for dataset in media audit accounts credentials sessions; do
  cmp "$validation_root/achrix_media_consumer-$dataset.jsonl" "$validation_root/achrix_media_restore-$dataset.jsonl"
done
cmp "$validation_root/achrix_media_consumer-ledgers.txt" "$validation_root/achrix_media_restore-ledgers.txt"
export NOTES_MEDIA_DATABASE_URL="host=$PGHOST port=$PGPORT user=$PGUSER dbname=achrix_media_restore sslmode=disable"
export NOTES_MEDIA_STORAGE_ROOT="$validation_root/media-restored"
NOTES_MEDIA_RESTORE_VERIFY=1 go test -race -count=1 -timeout=30s -run '^TestMediaTrustedRestore$' .

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
# Test binaries omit dependency build information on some Go toolchains. Build
# a real consumer executable so the runtime component identity is compared with
# the independently resolved, checksum-verified tag/pseudo-version above.
go build -trimpath -o "$validation_root/componentidentity" ./cmd/componentidentity
"$validation_root/componentidentity" > "$validation_root/components.json"
python3 - "$validation_root/foundation.json" "$validation_root/components.json" <<'PYCOMPONENT'
import json,sys
m=json.load(open(sys.argv[1])); b=json.load(open(sys.argv[2]))
assert b['foundation']==m['Version']
components={d['ID']: d for d in b['components']}
assert len(b['components'])==3 and set(components)=={'achrix.identity','achrix.audit','achrix.media'}
for d in components.values():
    assert d['Version']==m['Version'], 'packaged component version drift: '+d['ID']
    assert all(c['Version']==1 for c in d['Provides'])
    assert all(c['Version']==(2 if c['ID']=='achrix.authorization' else 1) for c in d['Requires'])
print('Verified packaged component versions:',json.dumps(b,sort_keys=True))
PYCOMPONENT
sha256sum "$validation_root/notes"
echo 'Foundation, isolated consumer, Identity/Audit/Media/Admin, PostgreSQL, migrations, authorization and coherent retained database/assets restore verified'
