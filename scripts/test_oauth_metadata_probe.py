# SPDX-License-Identifier: MPL-2.0
"""Fail-closed OAuth issuer metadata preflight contract regressions."""

import contextlib
import http.server
import json
import tempfile
import threading
import unittest
from io import StringIO
from pathlib import Path

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

    def test_unsupported_oidc_advertisement(self):
        for field in ("userinfo_endpoint", "id_token_signing_alg_values_supported"):
            doc = valid()
            doc[field] = "https://identity.example.test/unused"
            self.assertTrue(any(field in x for x in probe.validate(doc, ISSUER)))

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
