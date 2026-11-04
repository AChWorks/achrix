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
cat > "$scratch/main.go" <<'EOF'
package main

import (
    "fmt"
    "net/http"
    "net/http/httptest"
    "os"
    "net/url"

    "github.com/zitadel/oidc/v3/example/server/exampleop"
    "github.com/zitadel/oidc/v3/example/server/storage"
)

func main() {
    const issuer = "https://auth.example.test"
    storage.RegisterClients(storage.NativeClient("test", "https://client.example.test/callback"))
    handler := exampleop.SetupServer(issuer, storage.NewStorage(storage.NewUserStore(issuer)), nil, false)
    for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
        response := httptest.NewRecorder()
        handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, issuer+path, nil))
        fmt.Fprintf(os.Stderr, "DISCOVERY path=%s status=%d content_type=%q\n", path, response.Code, response.Header().Get("Content-Type"))
        if path == "/.well-known/openid-configuration" {
            if _, err := os.Stdout.Write(response.Body.Bytes()); err != nil { panic(err) }
        }
    }
    // The SDK demo config is intentionally not a product security profile.
    // Observe whether its HTTP authorization boundary enforces AChrix-required
    // S256 and resource presence before login, without completing a grant.
    for _, withS256 := range []bool{false, true} {
        args := url.Values{
            "response_type": {"code"}, "client_id": {"test"},
            "redirect_uri": {"https://client.example.test/callback"},
            "scope": {"openid"}, "state": {"demo-state"},
        }
        if withS256 {
            args.Set("code_challenge", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
            args.Set("code_challenge_method", "S256")
        }
        response := httptest.NewRecorder()
        handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, issuer+"/auth?"+args.Encode(), nil))
        location, err := url.Parse(response.Header().Get("Location"))
        if err != nil { panic(err) }
        fmt.Fprintf(os.Stderr, "AUTHORIZE with_s256=%v with_resource=false status=%d redirect_path=%s\n", withS256, response.Code, location.Path)
    }
}
EOF
(cd "$scratch" && GOWORK=off GOTOOLCHAIN=local go run -mod=mod .) > "$scratch/oidc.json" 2> "$scratch/provider.log"
cat "$scratch/provider.log"
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
echo 'This only probes pre-login acceptance; it proves no issued grant, exchange, refresh, persistence or real-client interoperability.'
