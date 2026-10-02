#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
set -euo pipefail
umask 077
if [[ $# != 0 ]]; then echo 'Usage: scripts/validate-admin-browser.sh' >&2; exit 2; fi
repository=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repository"
export GOWORK=off
if [[ $(id -u) == 0 ]]; then echo 'Run browser validation as a non-root user' >&2; exit 1; fi
if [[ $(go env GOVERSION) != go1.27.1 || $(go env GOSUMDB) == off ]]; then echo 'Go 1.27.1 with checksum verification is required' >&2; exit 1; fi
node=${ACHRIX_BROWSER_NODE:-node}
if [[ $("$node" --version) != v24.21.0 ]]; then echo 'Node 24.21.0 is required for this test-only browser profile' >&2; exit 1; fi
: "${ACHRIX_PLAYWRIGHT_MODULE:?set the installed Playwright module directory}"
: "${PLAYWRIGHT_BROWSERS_PATH:?set the installed Playwright browsers directory}"
"$node" -e 'if(require(process.env.ACHRIX_PLAYWRIGHT_MODULE+"/package.json").version!=="1.63.0")process.exit(1)'
if [[ -n ${ACHRIX_PG_BIN:-} ]]; then export PATH="$ACHRIX_PG_BIN:$PATH"; fi
for tool in initdb pg_ctl createdb; do
 if [[ $("$tool" --version | awk '{print $3}') != 18.6 ]]; then echo 'PostgreSQL 18.6 is required' >&2; exit 1; fi
done
validation_root=$(mktemp -d /tmp/achrix-admin-browser.XXXXXXXX)
started=0
fixture_pid=''
cleanup() {
 result=$?
 if [[ -n $fixture_pid ]]; then
  if [[ ! -f $validation_root/done ]]; then printf 'FAIL\n' > "$validation_root/done"; fi
  if ! wait "$fixture_pid"; then result=1; fi
  cat "$validation_root/fixture.log"
 fi
 if [[ $started == 1 ]]; then pg_ctl -D "$validation_root/database" -m fast -w stop >/dev/null || result=1; fi
 if [[ -d $validation_root/module-cache ]]; then GOMODCACHE="$validation_root/module-cache" go clean -modcache || result=1; fi
 rm -rf -- "$validation_root" || result=1
 exit "$result"
}
trap cleanup EXIT
mkdir "$validation_root/socket"
initdb -D "$validation_root/database" -A trust --no-locale -E UTF8 >/dev/null
pg_ctl -D "$validation_root/database" -l "$validation_root/postgres.log" -o "-k $validation_root/socket -h ''" -w start >/dev/null
started=1
PGHOST="$validation_root/socket" PGPORT=5432 PGUSER=$(id -un) createdb -T template0 achrix_admin_test
# Consumer-owned fixture source only: the complete SDK comes from Go resolution.
cp -R fixtures/notes "$validation_root/consumer"
export GOMODCACHE="$validation_root/module-cache"
cd "$validation_root/consumer"
go mod download
go mod verify
go list -m -json github.com/AChWorks/achrix > "$validation_root/foundation.json"
python3 - "$validation_root/foundation.json" "$repository" <<'PY'
import hashlib,json,os,pathlib,sys
m=json.load(open(sys.argv[1]));root=pathlib.Path(sys.argv[2]);dependency=pathlib.Path(m['Dir'])
assert m['Path']=='github.com/AChWorks/achrix' and 'Replace' not in m
assert m.get('Version','').startswith('v0.') and m.get('Sum') and m.get('GoModSum')
assert m['Dir'].startswith(os.environ['GOMODCACHE']+os.sep)
paths=[pathlib.Path('achrix.go')]
for package in ['identity','audit','media','admin']:
 assert (root/package).is_dir() and (dependency/package).is_dir(),'missing composed runtime package: '+package
 for p in sorted((root/package).rglob('*')):
  if p.is_file() and (p.suffix in ['.go','.sql','.js','.css','.html']) and not p.name.endswith('_test.go'):
   paths.append(p.relative_to(root))
digest=hashlib.sha256()
for relative in paths:
 source=(root/relative).read_bytes();assert source==(dependency/relative).read_bytes(),'normal-module source drift: '+str(relative)
 digest.update(str(relative).encode()+b'\0'+source)
print('Verified browser SDK pin:',m['Version'],m['Sum'],m['GoModSum'])
print('Verified Core/Identity/Audit/Media/Admin normal-module source SHA-256:',digest.hexdigest())
PY
go vet .
export NOTES_ADMIN_BROWSER=1 NOTES_ADMIN_BROWSER_ROOT="$validation_root"
export NOTES_ADMIN_TEST_DSN="host=$validation_root/socket port=5432 user=$(id -un) dbname=achrix_admin_test sslmode=disable"
go test -race -count=1 -timeout=240s -run '^TestAdminBrowserFixture$' -v . > "$validation_root/fixture.log" 2>&1 &
fixture_pid=$!
for ((attempt=0; attempt<900; attempt++)); do
 if [[ -f $validation_root/fixture.json ]]; then break; fi
 if ! kill -0 "$fixture_pid" 2>/dev/null; then cat "$validation_root/fixture.log" >&2; exit 1; fi
 sleep 0.1
done
if [[ ! -f $validation_root/fixture.json ]]; then echo 'Browser fixture startup deadline exceeded' >&2; exit 1; fi
NODE_EXTRA_CA_CERTS="$validation_root/fixture-ca.pem" "$node" "$repository/scripts/admin_browser.cjs" "$validation_root/fixture.json"
printf 'PASS\n' > "$validation_root/done"
wait "$fixture_pid"
fixture_pid=''
cat "$validation_root/fixture.log"
echo 'Actual HTTPS browser/normal SDK/Identity/Media/Admin proof passed'
