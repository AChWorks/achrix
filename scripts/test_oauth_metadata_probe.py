# SPDX-License-Identifier: MPL-2.0
"""Fail-closed OAuth issuer metadata preflight contract regressions."""

import contextlib
import http.server
import json
import tempfile
import threading
import unittest
from io import BytesIO, StringIO
from pathlib import Path
from unittest import mock

import oauth_metadata_probe as probe


ISSUER = "https://identity.example.test/tenant"


def valid():
    return {
        "issuer": ISSUER,
        "authorization_endpoint": "https://identity.example.test/tenant/authorize",
        "token_endpoint": "https://identity.example.test/tenant/token",
        "response_types_supported": ["code"],
        "grant_types_supported": ["authorization_code"],
        "response_modes_supported": ["query"],
        "code_challenge_methods_supported": ["S256"],
        "authorization_response_iss_parameter_supported": True,
        "token_endpoint_auth_methods_supported": ["none"],
    }


@contextlib.contextmanager
def serve(*, status=200, data=b"{}", ctype="application/json", location=None):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(status)
            self.send_header("Content-Type", ctype)
            if location:
                self.send_header("Location", location)
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *args):
            pass

    server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}"
    finally:
        server.shutdown()
        worker.join(timeout=2)
        server.server_close()


class MetadataContract(unittest.TestCase):
    def test_valid_minimum_profile(self):
        self.assertEqual(probe.validate(valid(), ISSUER), [])

    def test_conditional_refresh_and_confidential_auth(self):
        doc = valid()
        doc["grant_types_supported"].append("refresh_token")
        doc["token_endpoint_auth_methods_supported"].append("client_secret_basic")
        self.assertEqual(probe.validate(doc, ISSUER, allow_refresh=True,
                                         auth_methods=("none", "client_secret_basic")), [])
        self.assertTrue(probe.validate(doc, ISSUER))

    def test_rfc8414_required_or_omission_defaults(self):
        for field in ("response_types_supported", "grant_types_supported",
                      "response_modes_supported", "token_endpoint_auth_methods_supported"):
            with self.subTest(field=field):
                doc = valid()
                del doc[field]
                self.assertTrue(any(field in x for x in probe.validate(doc, ISSUER)))

    def test_misleading_grants_modes_and_response_types(self):
        for field, value in (
            ("response_types_supported", ["code", "token"]),
            ("response_types_supported", ["code", "id_token"]),
            ("grant_types_supported", ["authorization_code", "implicit"]),
            ("grant_types_supported", ["authorization_code", "password"]),
            ("response_modes_supported", ["query", "fragment"]),
            ("response_modes_supported", ["query", "form_post"]),
            ("token_endpoint_auth_methods_supported", ["none", "client_secret_post"]),
            ("code_challenge_methods_supported", ["S256", "plain"]),
        ):
            with self.subTest(field=field, value=value):
                doc = valid()
                doc[field] = value
                self.assertTrue(any(field in x for x in probe.validate(doc, ISSUER)))

    def test_missing_or_invalid_iss_flag(self):
        for flag in (None, False, "true", 1):
            with self.subTest(flag=flag):
                doc = valid()
                if flag is None:
                    del doc["authorization_response_iss_parameter_supported"]
                else:
                    doc["authorization_response_iss_parameter_supported"] = flag
                self.assertTrue(probe.validate(doc, ISSUER))

    def test_issuer_and_endpoint_spoofing(self):
        for field, value in (
            ("issuer", "https://evil.example.test"),
            ("authorization_endpoint", "http://identity.example.test/authorize"),
            ("token_endpoint", "https://user@identity.example.test/token"),
            ("token_endpoint", "https://identity.example.test/token#fragment"),
            ("token_endpoint", "https://identity.example.test:bad/token"),
        ):
            with self.subTest(field=field):
                doc = valid()
                doc[field] = value
                self.assertTrue(any(field in x for x in probe.validate(doc, ISSUER)))

    def test_duplicate_and_nonstring_fields(self):
        for field in ("response_types_supported", "grant_types_supported", "response_modes_supported"):
            for value in ([], ["code", "code"], [1], "code", None):
                with self.subTest(field=field, value=value):
                    doc = valid()
                    doc[field] = value
                    self.assertTrue(probe.validate(doc, ISSUER))

    def test_unsupported_oidc_and_extension_advertisement(self):
        for field in ("userinfo_endpoint", "id_token_signing_alg_values_supported",
                      "jwks_uri", "subject_types_supported", "claims_supported",
                      "registration_endpoint", "device_authorization_endpoint",
                      "end_session_endpoint", "introspection_endpoint"):
            doc = valid()
            doc[field] = "https://identity.example.test/unused"
            self.assertTrue(any(field in x for x in probe.validate(doc, ISSUER)))

    def test_unapproved_par_and_signed_metadata_are_rejected(self):
        for field, value in (
            ("pushed_authorization_request_endpoint", "https://identity.example.test/par"),
            ("require_pushed_authorization_requests", True),
            ("signed_metadata", "header.claims.signature"),
            ("signed_metadata", None),
        ):
            with self.subTest(field=field, value=value):
                doc = valid()
                doc[field] = value
                self.assertTrue(any(field in issue for issue in probe.validate(doc, ISSUER)))

    def test_security_relevant_optional_capabilities_fail_closed(self):
        for field in probe.UNAPPROVED_CAPABILITIES:
            with self.subTest(field=field):
                doc = valid()
                doc[field] = "https://identity.example.test/extra"
                self.assertTrue(any(field in issue for issue in probe.validate(doc, ISSUER)))
        for field in probe.UNAPPROVED_BOOLEAN_EXTENSIONS:
            for value in (True, "false", 0, None, 1):
                with self.subTest(field=field, value=value):
                    doc = valid()
                    doc[field] = value
                    self.assertTrue(any(field in issue for issue in probe.validate(doc, ISSUER)))

    def test_explicit_false_and_harmless_extensions_do_not_cause_false_positive(self):
        doc = valid()
        doc.update({field: False for field in probe.UNAPPROVED_BOOLEAN_EXTENSIONS})
        doc["service_documentation"] = "https://identity.example.test/help"
        doc["x_vendor_informational_note"] = "optional metadata"
        doc["scopes_supported"] = ["read"]
        self.assertEqual(probe.validate(doc, ISSUER), [])

    def test_scopes_supported_excludes_oidc_claims(self):
        doc = valid()
        doc["scopes_supported"] = ["read", "write"]
        self.assertEqual(probe.validate(doc, ISSUER), [])
        for value in (["openid", "read"], ["read", "read"], "read", [123]):
            with self.subTest(value=value):
                doc["scopes_supported"] = value
                self.assertTrue(any("scopes_supported" in issue for issue in
                                    probe.validate(doc, ISSUER)))

    def test_fixed_query_parameters_on_authorization_and_token_endpoints(self):
        # RFC 6749 permits fixed query components on endpoint URLs. They are
        # forbidden only on the issuer identifier itself.
        doc = valid()
        doc["authorization_endpoint"] += "?tenant=one"
        doc["token_endpoint"] += "?tenant=one"
        self.assertEqual(probe.validate(doc, ISSUER), [])
        doc["revocation_endpoint"] = "https://identity.example.test/revoke?tenant=one"
        doc["revocation_endpoint_auth_methods_supported"] = ["none"]
        self.assertEqual(probe.validate(doc, ISSUER), [])

    def test_revocation_defaults_must_not_be_inferred(self):
        doc = valid()
        doc["revocation_endpoint"] = "https://identity.example.test/revoke"
        self.assertTrue(probe.validate(doc, ISSUER))
        doc["revocation_endpoint_auth_methods_supported"] = ["none"]
        self.assertFalse(probe.validate(doc, ISSUER))

    def test_duplicate_json_keys_and_payload_bounds(self):
        with self.assertRaisesRegex(probe.ProbeError, "duplicate"):
            probe.read_json(b'{"issuer":"a","issuer":"b"}')
        with self.assertRaisesRegex(probe.ProbeError, "64 KiB"):
            probe.read_json(b"x" * (probe.MAX_BYTES + 1))
        with self.assertRaises(probe.ProbeError):
            probe.read_json(b"[]")
        with self.assertRaises(probe.ProbeError):
            probe.read_json(b'{"a": [not-json]}')

    def test_nonstandard_json_numbers_and_overflow_fail(self):
        for literal in ("NaN", "Infinity", "-Infinity", "1e9999"):
            with self.subTest(literal=literal), self.assertRaisesRegex(probe.ProbeError, "non-finite"):
                probe.read_json(("{\"x_info\":" + literal + "}").encode())

    def test_offline_file_read_is_bounded_before_allocation(self):
        payload = b"x" * (probe.MAX_BYTES + 2)
        class CountingReader(BytesIO):
            def read(self, size=-1):
                self.requested_size = size
                return super().read(size)
        stream = CountingReader(payload)
        with mock.patch.object(Path, "open", return_value=stream) as open_file:
            with self.assertRaisesRegex(probe.ProbeError, "64 KiB"):
                probe.read_metadata_file(Path("unused.json"))
            open_file.assert_called_once_with("rb")
            self.assertEqual(stream.requested_size, probe.MAX_BYTES + 1)
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / "oversized.json"
            file.write_bytes(payload)
            with contextlib.redirect_stderr(StringIO()):
                self.assertEqual(probe.main(["--issuer", ISSUER, "--metadata-file", str(file)]), 1)

    def test_duplicate_json_key_error_does_not_echo_untrusted_text(self):
        key = "sensitive-control-" + chr(10)
        encoded = json.dumps({key: 1})
        malformed = encoded[:-1] + "," + encoded[1:]
        with self.assertRaises(probe.ProbeError) as context:
            probe.read_json(malformed.encode())
        self.assertEqual(str(context.exception), "duplicate JSON property")
        self.assertNotIn("sensitive-control", str(context.exception))

    def test_rfc8414_discovery_issuer_path(self):
        self.assertEqual(probe.discovery_url(ISSUER),
                         "https://identity.example.test/.well-known/oauth-authorization-server/tenant")
        self.assertEqual(probe.discovery_url("https://identity.example.test"),
                         "https://identity.example.test/.well-known/oauth-authorization-server")

    def test_reject_invalid_issuer_urls(self):
        for value in ("http://identity.example.test", "https://user@identity.example.test",
                      "https://identity.example.test?q=1", "https://identity.example.test/#f",
                      "https://identity.example.test:65536/", "https://identity.example.test/a/../b",
                      "https://identity.example.test//x", "ftp://identity.example.test", "bad"):
            with self.subTest(value=value), self.assertRaises(probe.ProbeError):
                probe.discovery_url(value)

    def test_raw_controls_whitespace_and_empty_delimiters_are_rejected(self):
        for value in ("\n" + ISSUER, "\t" + ISSUER, " " + ISSUER,
                      ISSUER + "\r", ISSUER + "\x00", ISSUER + "\x7f",
                      ISSUER + "\u0085", ISSUER + "\u00a0", ISSUER + "?", ISSUER + "#"):
            with self.subTest(issuer=repr(value)), self.assertRaises(probe.ProbeError):
                probe.discovery_url(value)
        for field in ("authorization_endpoint", "token_endpoint", "revocation_endpoint"):
            for suffix in ("?", "#", "?tenant=one#", "\n", "\t", "\x00", "\u00a0"):
                with self.subTest(field=field, suffix=repr(suffix)):
                    doc = valid()
                    doc[field] = "https://identity.example.test/path" + suffix
                    if field == "revocation_endpoint":
                        doc["revocation_endpoint_auth_methods_supported"] = ["none"]
                    self.assertTrue(any(field in issue for issue in probe.validate(doc, ISSUER)))
        doc = valid()
        doc["authorization_endpoint"] += "?tenant=one&encoded=%20"
        self.assertEqual(probe.validate(doc, ISSUER), [])

    def test_client_auth_selection_must_be_explicit(self):
        for methods in ((), ("none", "none"), ("none", "client_secret_post")):
            with self.subTest(methods=methods), self.assertRaises(probe.ProbeError):
                probe.validate(valid(), ISSUER, auth_methods=methods)

    def test_fetch_enforces_200_json_no_redirect_and_bounds(self):
        doc = json.dumps(valid()).encode()
        with serve(data=doc) as url:
            self.assertEqual(probe.fetch(url, allow_local_http=True), valid())
        for config in ({"status": 404}, {"ctype": "text/html"},
                       {"status": 302, "location": "http://127.0.0.1:1/"},
                       {"data": b"x" * (probe.MAX_BYTES + 1)}):
            with self.subTest(config=config.keys()), serve(**config) as url:
                with self.assertRaises(probe.ProbeError):
                    probe.fetch(url, allow_local_http=True)

    def test_local_http_is_only_permitted_by_explicit_flag(self):
        with serve(data=b"{}") as url:
            with self.assertRaises(probe.ProbeError):
                probe.fetch(url)

    def test_cli_offline_success_and_invalid(self):
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / "metadata.json"
            file.write_text(json.dumps(valid()))
            with contextlib.redirect_stdout(StringIO()), contextlib.redirect_stderr(StringIO()):
                self.assertEqual(probe.main(["--issuer", ISSUER, "--metadata-file", str(file)]), 0)
            doc = valid()
            doc["grant_types_supported"].append("implicit")
            file.write_text(json.dumps(doc))
            with contextlib.redirect_stdout(StringIO()), contextlib.redirect_stderr(StringIO()):
                self.assertEqual(probe.main(["--issuer", ISSUER, "--metadata-file", str(file)]), 1)


if __name__ == "__main__":
    unittest.main()
