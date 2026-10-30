// SPDX-License-Identifier: MPL-2.0
package audit

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AChWorks/achrix"
	"github.com/jackc/pgx/v5/pgtype"
)

// Attribution is trusted product-bound request provenance, not authorization.
// Actor remains Record.Actor. Client and connection IDs are non-secret opaque
// identifiers, never access tokens, credentials, headers or arbitrary payloads.
// A missing connection ID is permitted; every other field is required.
type Attribution struct {
	ClientID     string
	ConnectionID string
	Channel      string
	RequestID    string
	Operation    string
}

type attributionKey struct{}
type boundAttribution struct {
	actor achrix.Principal
	value Attribution
}

// WithAttribution validates and copies verifier-bound attribution for one actor.
// Trusted entry-point composition supplies this value after authentication; it
// must never be decoded from public operation arguments or identity headers.
// It grants no access and preserves the original cancellation/deadline. Prepare
// checks the actor binding and captures its own immutable value snapshot.
// Existing direct calls need not attach attribution; absence stays explicit.
func WithAttribution(ctx context.Context, actor achrix.Principal, value Attribution) (context.Context, error) {
	if ctx == nil {
		return nil, configuration("context", "requires a non-nil parent")
	}
	if !validReference(string(actor)) {
		return nil, &InputError{"attribution.actor", "invalid_reference"}
	}
	if err := validateAttribution(value); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, attributionKey{}, boundAttribution{actor: actor, value: value}), nil
}

func attributionFor(ctx context.Context, actor achrix.Principal) (*Attribution, error) {
	bound, exists := ctx.Value(attributionKey{}).(boundAttribution)
	if !exists {
		return nil, nil
	}
	if bound.actor != actor {
		return nil, &InputError{"attribution.actor", "actor_mismatch"}
	}
	snapshot := bound.value
	return &snapshot, nil
}

func validateAttribution(value Attribution) error {
	if value.ClientID == "" || !attributionText(value.ClientID, 256) {
		return &InputError{"attribution.client_id", "must_be_1_to_256_utf8_bytes_without_controls"}
	}
	if !attributionText(value.ConnectionID, 256) {
		return &InputError{"attribution.connection_id", "must_be_at_most_256_utf8_bytes_without_controls"}
	}
	if !validReference(value.RequestID) {
		return &InputError{"attribution.request_id", "invalid_reference"}
	}
	if !attributionCode(value.Operation, 96) {
		return &InputError{"attribution.operation", "must_be_1_to_96_code_characters"}
	}
	if !attributionCode(value.Channel, 32) {
		return &InputError{"attribution.channel", "must_be_1_to_32_code_characters"}
	}
	return nil
}

func attributionText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func attributionCode(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// SQL NULLs distinguish historical/direct records from attributed operations.
// A malformed persisted tuple is a storage failure, never caller input guidance.
func retainedAttribution(columns [5]pgtype.Text) (*Attribution, error) {
	any := false
	for _, column := range columns {
		any = any || column.Valid
	}
	if !any {
		return nil, nil
	}
	value := Attribution{ClientID: columns[0].String, ConnectionID: columns[1].String, Channel: columns[2].String, RequestID: columns[3].String, Operation: columns[4].String}
	if !columns[0].Valid || !columns[2].Valid || !columns[3].Valid || !columns[4].Valid || validateAttribution(value) != nil {
		return nil, diagnosticFault("invalid_record_attribution", "retained attribution violates the owned schema", ErrUnavailable)
	}
	return &value, nil
}
