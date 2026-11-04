#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
# Disposable, pinned non-production dependency/protocol compatibility proof.
# Imports the upstream *example* only in a private temporary Go module.
# The example's in-memory storage and demo credentials are not product code.
# Only the private source/scratch directory is deleted: standard Go module/build
# caches may persist and are shared, so this is not cache-isolated execution.
set -Eeuo pipefail
umask 077
repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
export PATH="$(go env GOROOT)/bin:$PATH"
if [[ $(go env GOVERSION) != go1.27.3 || $(go env GOSUMDB) == off ]]; then
  echo 'Go 1.27.3 and checksum verification are required' >&2
  exit 1
fi
scratch=$(mktemp -d /tmp/achrix-oidc-live-proof.XXXXXXXX)
trap 'rm -rf -- "$scratch"' EXIT
cat > "$scratch/go.mod" <<'EOF'
module example.invalid/achrix-oidc-discovery-proof

go 1.27.3

require github.com/zitadel/oidc/v3 v3.51.17
EOF
cp "$repo/scripts/testdata/zitadel_oidc_runtime/main.go" "$scratch/main.go"
if ! (cd "$scratch" && GOWORK=off GOTOOLCHAIN=local go run -mod=mod .) > "$scratch/oidc.json" 2> "$scratch/provider.log"; then
  cat "$scratch/provider.log" >&2
  echo "Live upstream probe failed; see diagnostics above" >&2
  exit 1
fi
grep -E "^(DISCOVERY|AUTHORIZE|RESOURCE_MODEL|CODE_FLOW) " "$scratch/provider.log"
if ! grep -q '^DISCOVERY path=/.well-known/oauth-authorization-server status=404 ' "$scratch/provider.log"; then
  echo 'The tested SDK discovery behavior changed; repeat the compatibility assessment' >&2
  exit 1
fi
if ! grep -q '^AUTHORIZE with_s256=false with_resource=false status=302 redirect_path=/login/username' "$scratch/provider.log"; then
  echo 'The tested SDK authorization acceptance behavior changed; repeat the security assessment' >&2
  exit 1
fi
python3 - "$scratch/oidc.json" <<'PY'
import json, sys
metadata = json.load(open(sys.argv[1]))
for field in ('issuer', 'response_types_supported', 'grant_types_supported',
              'response_modes_supported', 'token_endpoint_auth_methods_supported',
              'authorization_response_iss_parameter_supported', 'code_challenge_methods_supported'):
    print(f'{field}={metadata.get(field, "<omitted>")!r}')
PY
if python3 "$repo/scripts/oauth_metadata_probe.py" \
    --issuer https://auth.example.test --metadata-file "$scratch/oidc.json" \
    --auth-method none --auth-method client_secret_basic; then
  echo 'The previously rejected upstream default became profile-conforming; reassess the vendor' >&2
  exit 1
fi
echo 'PROOF PASS: SDK v3.51.17 example defaults fail the first-profile metadata contract and accept an authorization request without required S256/resource before login.'
echo 'Actual SDK example code exchange was exercised: S256 succeeds; missing PKCE and bad verifier/redirect fail; code replay fails in demo memory only.'
echo 'Missing/mismatched RFC 8707 resource still results in token issuance; no RFC 9207 iss in the demo redirect. This is a NEGATIVE security-fit proof, not issuer approval.'
echo 'Sample user/login/storage and tokens are disposable. No product Identity, durable PostgreSQL, consent CSRF, audience proof or real-client interoperability is established.'
