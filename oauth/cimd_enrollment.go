// SPDX-License-Identifier: MPL-2.0
package oauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

// maxCIMDDocumentSize bounds operator-supplied metadata parsing. It is NOT
// an HTTP fetch limit; the product must also enforce limits while downloading.
const maxCIMDDocumentSize = 64 << 10

// ValidatePreEnrolledClientMetadata checks that a trusted, already retrieved
// CIMD JSON document is compatible with an operator-approved URL Client.
//
// This performs no network, DNS, redirects, registration or approvals. It does
// not establish a client identity by itself and must never run as a response
// to unknown OAuth client_id values. Products own safe HTTPS retrieval,
// trustworthy review/approval, durable enrollment records and periodic
// revalidation. On drift, their ClientLookup must fail closed and remain
// disabled until a deliberate re-approval.
//
// The bounded initial profile supports only public URL clients using "none"
// authentication. It tolerates the metadata's additional private_key_jwt
// preference when token_endpoint_auth_methods_supported also offers "none".
// Errors are safe operator diagnostics, never serialized to public OAuth clients.
func ValidatePreEnrolledClientMetadata(c Client, document []byte) error {
	if !strings.HasPrefix(c.ID, "https://") || !validClientID(c.ID) ||
		c.Confidential || c.SecretDigest != ([32]byte{}) ||
		len(c.RedirectURIs) == 0 || len(c.RedirectURIs) > 12 {
		return fmt.Errorf("%w: unsupported URL client registration", ErrConfiguration)
	}
	registered := make(map[string]bool, len(c.RedirectURIs))
	for _, redirect := range c.RedirectURIs {
		if registered[redirect] || validateRegisteredRedirect(redirect) != nil {
			return fmt.Errorf("%w: duplicate or invalid registered callback", ErrConfiguration)
		}
		registered[redirect] = true
	}
	fields, err := parseCIMDTopLevel(document)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConfiguration, err)
	}
	for _, forbidden := range []string{"client_secret", "client_secret_expires_at", "software_statement", "jwks"} {
		if _, exists := fields[forbidden]; exists {
			// No symmetric secrets, delegated registration statements or
			// unexamined embedded key material in this bounded profile.
			return fmt.Errorf("%w: unsupported credential metadata", ErrConfiguration)
		}
	}
	var id string
	if err := json.Unmarshal(fields["client_id"], &id); err != nil || id != c.ID {
		return fmt.Errorf("%w: client_id differs from approved URL", ErrConfiguration)
	}
	var published []string
	if err := json.Unmarshal(fields["redirect_uris"], &published); err != nil || len(published) == 0 || len(published) > 64 {
		return fmt.Errorf("%w: invalid metadata redirect_uris", ErrConfiguration)
	}
	seen := make(map[string]bool, len(published))
	for _, redirect := range published {
		if redirect == "" || seen[redirect] {
			return fmt.Errorf("%w: duplicated or empty metadata callback", ErrConfiguration)
		}
		seen[redirect] = true
	}
	for redirect := range registered {
		if !seen[redirect] {
			return fmt.Errorf("%w: approved callback removed from metadata", ErrConfiguration)
		}
	}
	if err := compatibleCIMDAuth(fields); err != nil {
		return fmt.Errorf("%w: %v", ErrConfiguration, err)
	}
	if err := optionalCIMDMember(fields, "response_types", "code"); err != nil {
		return fmt.Errorf("%w: %v", ErrConfiguration, err)
	}
	if err := optionalCIMDMember(fields, "grant_types", "authorization_code"); err != nil {
		return fmt.Errorf("%w: %v", ErrConfiguration, err)
	}
	return nil
}

// parseCIMDTopLevel rejects duplicate top-level keys because encoding/json's
// ordinary map decoding otherwise silently retains the last security field.
func parseCIMDTopLevel(document []byte) (map[string]json.RawMessage, error) {
	if len(document) == 0 || len(document) > maxCIMDDocumentSize || !utf8.Valid(document) {
		return nil, fmt.Errorf("metadata body missing, oversized or invalid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(document))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("metadata must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for dec.More() {
		token, err := dec.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, fmt.Errorf("invalid metadata property")
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, fmt.Errorf("duplicate metadata property")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("invalid metadata property value")
		}
		fields[key] = raw
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return nil, fmt.Errorf("invalid metadata object ending")
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected metadata content after object")
	}
	return fields, nil
}

func compatibleCIMDAuth(fields map[string]json.RawMessage) error {
	// draft-03 requires none by default for URL clients; the RFC 7591
	// default client_secret_basic must not be applied to CIMD documents.
	preferred := ""
	if raw, present := fields["token_endpoint_auth_method"]; present {
		if err := json.Unmarshal(raw, &preferred); err != nil || !validDeclaredCIMDMethod(preferred) {
			return fmt.Errorf("invalid legacy client authentication preference")
		}
	}
	raw, hasSupported := fields["token_endpoint_auth_methods_supported"]
	if !hasSupported {
		if preferred != "" && preferred != "none" {
			return fmt.Errorf("metadata does not support public authentication")
		}
		return nil
	}
	var supported []string
	if err := json.Unmarshal(raw, &supported); err != nil || len(supported) == 0 || len(supported) > 8 {
		return fmt.Errorf("invalid client authentication methods")
	}
	seen := make(map[string]bool, len(supported))
	for _, method := range supported {
		if !validDeclaredCIMDMethod(method) || seen[method] {
			return fmt.Errorf("invalid, shared-secret or duplicated client authentication method")
		}
		seen[method] = true
	}
	if preferred != "" && !slices.Contains(supported, preferred) {
		return fmt.Errorf("legacy preference is absent from supported methods")
	}
	if !seen["none"] {
		return fmt.Errorf("public client authentication unavailable")
	}
	return nil
}

// The selected method is always "none". Per the CIMD draft, additional
// unselected, well-formed methods must not invalidate a client that also
// offers "none": doing so would gratuitously reject future asymmetric
// methods. Shared-secret client methods are invalid for CIMD, irrespective
// of the selected method. Neither rule enables any new token auth method.
func validDeclaredCIMDMethod(method string) bool {
	if len(method) == 0 || len(method) > 128 || strings.HasPrefix(method, "client_secret_") {
		return false
	}
	for i := 0; i < len(method); i++ {
		if method[i] < 0x21 || method[i] > 0x7e {
			return false
		}
	}
	return true
}

func optionalCIMDMember(fields map[string]json.RawMessage, key, required string) error {
	raw, present := fields[key]
	if !present { // RFC 7591 defaults for these fields are code/authorization_code.
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 || len(values) > 16 {
		return fmt.Errorf("invalid client %s", key)
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			return fmt.Errorf("duplicate or invalid client %s", key)
		}
		seen[value] = true
	}
	if !seen[required] {
		return fmt.Errorf("client %s excludes required %s", key, required)
	}
	return nil
}
