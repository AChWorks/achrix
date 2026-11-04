#!/usr/bin/env python3
# SPDX-License-Identifier: MPL-2.0
"""Offline or HTTPS-only issuer discovery preflight for Issue #125.

This validates *advertised* RFC 8414/RFC 9207 capabilities against AChrix's
first proposed OAuth profile, not a working issuer, token security, or client
interoperability. It does not fetch optional endpoint URLs or send credentials.

Usage:
  python3 scripts/oauth_metadata_probe.py --issuer https://auth.example.test \
      --metadata-file /path/to/metadata.json --auth-method none
  python3 scripts/oauth_metadata_probe.py --issuer https://auth.example.test \
      --fetch --auth-method none --auth-method client_secret_basic

Network fetching requires an explicit --fetch and TLS verification; there is
no insecure-TLS option and redirects are rejected. Set --allow-loopback-http
only for a disposable local HTTP fixture, never as a deployment configuration.
"""

import argparse
import json
import math
import sys
import unicodedata
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

MAX_BYTES = 65536
MAX_ISSUER_LENGTH = 2048

# Only recognized extensions that would alter the first supported OAuth
# profile are excluded. Informational and unknown vendor metadata are allowed.
UNAPPROVED_CAPABILITIES = (
    "userinfo_endpoint", "id_token_signing_alg_values_supported",
    "jwks_uri", "subject_types_supported", "claims_supported",
    "registration_endpoint", "device_authorization_endpoint",
    "end_session_endpoint", "introspection_endpoint",
    "pushed_authorization_request_endpoint", "signed_metadata",
    "request_object_signing_alg_values_supported",
    "authorization_details_types_supported", "dpop_signing_alg_values_supported",
    "mtls_endpoint_aliases", "token_endpoint_auth_signing_alg_values_supported",
    "revocation_endpoint_auth_signing_alg_values_supported",
    "introspection_endpoint_auth_methods_supported", "backchannel_authentication_endpoint",
)

# An explicit False states that an extension is unavailable; it is not itself
# an additional capability. True or non-Boolean values fail closed.
UNAPPROVED_BOOLEAN_EXTENSIONS = (
    "require_pushed_authorization_requests", "request_parameter_supported",
    "request_uri_parameter_supported", "require_signed_request_object",
    "tls_client_certificate_bound_access_tokens", "frontchannel_logout_supported",
    "backchannel_logout_supported", "backchannel_logout_session_supported",
)


class ProbeError(ValueError):
    """Input or profile mismatch; contains no token or credential values."""


def _parse_url(value, *, allow_local_http=False, allow_query=False):
    if not isinstance(value, str) or not value or len(value) > MAX_ISSUER_LENGTH:
        raise ProbeError("URL must be a bounded nonempty string")
    # urlsplit strips raw C0/space prefixes and CR/LF/TAB anywhere. Check the
    # original string first so different inputs never normalize to one URL.
    if any(unicodedata.category(char) == "Cc" or char.isspace() for char in value):
        raise ProbeError("raw control or whitespace character in URL")
    # urlsplit does not preserve bare '?' and '#' delimiters in .query/.fragment.
    # An issuer has neither; an endpoint may have a nonempty fixed query but
    # never a fragment (including a bare '#').
    if "#" in value or ("?" in value and (not allow_query or value.split("?", 1)[1] == "")):
        raise ProbeError("URL contains a forbidden query or fragment delimiter")
    try:
        parsed = urllib.parse.urlsplit(value)
        port = parsed.port
    except ValueError as error:
        raise ProbeError("invalid URL") from error
    if parsed.username is not None or parsed.password is not None:
        raise ProbeError("URL userinfo is forbidden")
    if not parsed.hostname or (port is not None and port == 0):
        raise ProbeError("URL must have a host and a valid port")
    if parsed.scheme == "https":
        return parsed
    if parsed.scheme == "http" and allow_local_http and parsed.hostname in ("localhost", "127.0.0.1", "::1"):
        return parsed
    raise ProbeError("HTTPS is required (loopback HTTP requires explicit local-test flag)")


def discovery_url(issuer, *, allow_local_http=False):
    url = _parse_url(issuer, allow_local_http=allow_local_http)
    if url.path.startswith("//") or "/../" in url.path or "/./" in url.path:
        raise ProbeError("issuer path is not canonical")
    # RFC 8414 section 3: insert the well-known path between authority and
    # the issuer path component; do not append after the issuer path.
    return urllib.parse.urlunsplit((url.scheme, url.netloc,
        "/.well-known/oauth-authorization-server" + url.path.rstrip("/"), "", ""))


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ProbeError("duplicate JSON property")
        result[key] = value
    return result


def _reject_nonfinite(_):
    raise ProbeError("non-finite JSON number")


def _finite_float(value):
    number = float(value)
    if not math.isfinite(number):
        raise ProbeError("non-finite JSON number")
    return number


def read_json(data):
    if len(data) > MAX_BYTES:
        raise ProbeError("metadata response exceeds 64 KiB")
    try:
        # RFC 8259 requires UTF-8 for network JSON. Parse decoded text so
        # Python cannot auto-detect and accept UTF-16/UTF-32 byte streams.
        result = json.loads(data.decode("utf-8", errors="strict"),
                            object_pairs_hook=_unique_object,
                            parse_constant=_reject_nonfinite, parse_float=_finite_float)
    except ProbeError:
        raise
    except (ValueError, RecursionError) as error:
        # Python JSON parsers can reject bounded inputs via integer-digit or
        # nesting limits; never expose an uncaught traceback to callers.
        raise ProbeError("invalid metadata JSON") from error
    if not isinstance(result, dict):
        raise ProbeError("metadata must be a JSON object")
    return result


def read_metadata_file(path):
    # Bound the read before allocation, exactly like the HTTPS response path.
    with path.open("rb") as metadata_file:
        return read_json(metadata_file.read(MAX_BYTES + 1))


def _array(metadata, key, expected):
    # Explicit presence is mandatory: omission defaults in RFC 8414 can claim
    # implicit, fragment, or client_secret_basic, contrary to this profile.
    value = metadata.get(key)
    if not isinstance(value, list) or not value or any(not isinstance(x, str) or not x for x in value):
        return f"{key}: explicit nonempty string array required"
    if len(set(value)) != len(value):
        return f"{key}: duplicate values"
    if set(value) != set(expected):
        return f"{key}: must exactly match selected, implemented profile"
    return None


def validate(metadata, issuer, *, allow_refresh=False, auth_methods=("none",), allow_local_http=False):
    """Return safe, deterministic violations; never claim runtime behavior."""
    _parse_url(issuer, allow_local_http=allow_local_http)
    if not isinstance(metadata, dict):
        raise ProbeError("metadata must be a JSON object")
    if not auth_methods or len(set(auth_methods)) != len(auth_methods) or any(
        x not in ("none", "client_secret_basic") for x in auth_methods
    ):
        raise ProbeError("selected initial-profile token auth methods must be none and/or client_secret_basic")
    problems = []
    if metadata.get("issuer") != issuer:
        problems.append("issuer: does not exactly match the configured issuer")
    for field in ("authorization_endpoint", "token_endpoint"):
        value = metadata.get(field)
        try:
            # RFC 6749 permits fixed query parameters on endpoint URLs, but
            # never on the issuer identifier or discovery URL itself.
            _parse_url(value, allow_local_http=allow_local_http, allow_query=True)
        except ProbeError:
            problems.append(field + ": canonical HTTPS endpoint required")
    arrays = {
        "response_types_supported": ("code",),
        "grant_types_supported": ("authorization_code", "refresh_token") if allow_refresh else ("authorization_code",),
        "response_modes_supported": ("query",),
        "code_challenge_methods_supported": ("S256",),
        "token_endpoint_auth_methods_supported": tuple(auth_methods),
    }
    for name, expected in arrays.items():
        issue = _array(metadata, name, expected)
        if issue:
            problems.append(issue)
    if metadata.get("authorization_response_iss_parameter_supported") is not True:
        problems.append("authorization_response_iss_parameter_supported: explicit true required")
    # RFC 8414 signed_metadata may override plain JSON values for clients that
    # process it; this first-profile proof cannot validate those signed claims.
    # Reject specifically known, materially different OAuth/OIDC capabilities
    # without incorrectly rejecting harmless or informational vendor fields.
    for name in UNAPPROVED_CAPABILITIES:
        if name in metadata:
            problems.append(name + ": unapproved first-profile capability advertised")
    for name in UNAPPROVED_BOOLEAN_EXTENSIONS:
        if name in metadata and metadata[name] is not False:
            problems.append(name + ": unapproved extension required or invalid Boolean")
    if "scopes_supported" in metadata:
        scopes = metadata["scopes_supported"]
        if (not isinstance(scopes, list) or
                any(not isinstance(scope, str) or not scope for scope in scopes) or
                len(set(scopes)) != len(scopes) or "openid" in scopes):
            problems.append("scopes_supported: invalid or unapproved OIDC scope advertised")
    if "revocation_endpoint" in metadata:
        try:
            _parse_url(metadata["revocation_endpoint"], allow_local_http=allow_local_http, allow_query=True)
        except ProbeError:
            problems.append("revocation_endpoint: canonical HTTPS endpoint required")
        # The revocation endpoint may support a narrower set than token exchange;
        # an omitted or broader set would claim methods we have not selected.
        revocation = metadata.get("revocation_endpoint_auth_methods_supported")
        if (not isinstance(revocation, list) or not revocation or
                any(not isinstance(value, str) or not value for value in revocation) or
                len(set(revocation)) != len(revocation) or
                not set(revocation).issubset(set(auth_methods))):
            problems.append("revocation_endpoint_auth_methods_supported: explicit selected methods required")
    return problems


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, newurl):
        raise ProbeError("discovery redirects are not accepted")


def fetch(url, *, allow_local_http=False):
    _parse_url(url, allow_local_http=allow_local_http)
    request = urllib.request.Request(url, method="GET", headers={"Accept": "application/json"})
    opener = urllib.request.build_opener(_NoRedirect())
    try:
        with opener.open(request, timeout=8) as response:
            if response.status != 200:
                raise ProbeError("metadata retrieval must return HTTP 200")
            if response.headers.get_content_type() != "application/json":
                raise ProbeError("metadata must use application/json")
            return read_json(response.read(MAX_BYTES + 1))
    except urllib.error.HTTPError as error:
        raise ProbeError(f"metadata retrieval returned HTTP {error.code}") from error
    except (OSError, urllib.error.URLError) as error:
        raise ProbeError("metadata retrieval failed: transport or TLS verification") from error


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--issuer", required=True)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--metadata-file", type=Path)
    mode.add_argument("--fetch", action="store_true")
    parser.add_argument("--auth-method", choices=("none", "client_secret_basic"), action="append")
    parser.add_argument("--allow-refresh", action="store_true")
    parser.add_argument("--allow-loopback-http", action="store_true")
    args = parser.parse_args(argv)
    try:
        url = discovery_url(args.issuer, allow_local_http=args.allow_loopback_http)
        metadata = (fetch(url, allow_local_http=args.allow_loopback_http) if args.fetch else
                    read_metadata_file(args.metadata_file))
        problems = validate(metadata, args.issuer,
                            auth_methods=tuple(args.auth_method or ("none",)),
                            allow_refresh=args.allow_refresh,
                            allow_local_http=args.allow_loopback_http)
    except (ProbeError, OSError) as error:
        print("FAIL: " + str(error), file=sys.stderr)
        return 1
    if problems:
        for problem in problems:
            print("FAIL: " + problem, file=sys.stderr)
        print("Metadata-only profile preflight FAILED; no runtime security claim.", file=sys.stderr)
        return 1
    print("PASS: issuer metadata matches selected first-profile advertisement (metadata only).")
    print("Not proof of endpoint behavior, login/consent, PKCE exchange, token safety, or client interoperability.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
