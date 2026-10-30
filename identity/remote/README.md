# Optional Identity HTTP and MCP administration

Import `github.com/AChWorks/achrix/identity/remote` only when the product needs network account administration. Direct `identity.Service` calls remain available without Bridge/MCP imports. `NewHTTP(service, cfg)` and `NewMCP(service, cfg)` return explicitly mounted Bridge endpoints; `Register` adds the two tools to a product-composed Bridge. Supply separate canonical HTTP/MCP resources and credential verifiers for their audiences, current connection admission and any narrower channel gates. Construction starts no listeners or per-connection workers.

| Consumer outcome / owning operation | HTTP below configured resource | MCP tool |
| --- | --- | --- |
| Inspect one account and its precondition / `Account` | `GET /accounts/{account_id}` | `achrix.identity.account.read` |
| Set enabled state, revoke sessions and append atomic Audit / `SetEnabled` | `POST /accounts/{account_id}/enabled` | `achrix.identity.account.set-enabled` |

The [OpenAPI 3.1.1 contract](openapi.json), also available as a copy from `OpenAPI()`, supplies the explicit SDK input/output schema fragments. Its server URL is an example; replace it with the product's configured resource. No actor, client, channel or request claims are accepted in inputs. Tokens, passwords, account creation, login/consent, password changes, permission management and account listing are outside this selected tool profile. Credential-bearing authentication ceremonies remain on their dedicated secure Identity/product routes; additional administration operations need an actual consumer and deliberate contract.

## Conditional account workflow

An authorized caller reads one account, preserves its **decimal-string** revision and submits a conditional change:

```http
POST /api/identity/v1/accounts/ABCDEFGHIJKLMNOPQRSTUVWXYZ/enabled
Content-Type: application/json
Authorization: Bearer <credential for this HTTP resource>

{"expected_revision":"9007199254740993","enabled":false}
```

Equivalent MCP arguments are `{"account_id":"ABCDEFGHIJKLMNOPQRSTUVWXYZ","expected_revision":"9007199254740993","enabled":false}`. Never convert the revision to a floating-point/JSON number. Both fields are required; false is a real value. HTTP accepts one UTF-8 JSON object up to 4096 bytes, rejects duplicate/unknown fields, trailing values, unsupported query parameters and content encoding. MCP uses the same typed field rules and the Bridge request budget.

The owning Service authorizes the exact account before lookup. Account reads preserve the existing conflict response for an authorized absent ID; denial does not reveal existence. SetEnabled requires both the current exact-account status-change permission and Audit append permission. It does not require an extra AccountRead permission. Every successful call increments the account revision, revokes every session and appends one Audit event in the same native transaction, including a request that repeats the current enabled value. It is therefore not idempotent. The success result confirms the account ID/outcome and performs no implicit follow-up read; a separately authorized Account call can inspect current state.

## Trusted durable attribution and migration

Before invoking SetEnabled, the adapter maps only `bridge.RequestFromContext` to the Audit-owned actor-bound `WithAttribution` API. The verified principal remains `Record.Actor`; client ID, optional nonsecret connection ID, endpoint-selected channel, generated request ID and fixed operation name are snapshotted into the atomic record. Forwarded headers, MCP clientInfo and submitted JSON cannot provide them. The product verifier must map subjects to valid internal Audit principals; the adapter does not truncate or hash a broader external subject into an account. Client/connection identifiers must be nonsecret labels, not credentials.

This composition requires Audit's explicit version-2 attribution migration before starting compatible Identity/Audit binaries. See [Audit](../../audit/README.md) for immutable history, verified v1 upgrade, truthful absent legacy attribution and recovery. No request or constructor migrates a database. Identity's own schema and public service signatures are unchanged.

## Errors, uncertainty and recovery limits

HTTP uses bounded Problem Details; MCP failed tools carry the same safe code/message/action/fields/outcome/reference/request ID representation, outside the success schema. Source-owned input reasons are retained without echoing submitted values. Protected callbacks retain original provider causes and trusted request context; ordinary output does not expose SQL, credentials or private provider errors.

Pre-effect input/permission/readiness rejections report `not_executed`. Reads change no state. Identity's coarse public mutation errors do not always expose transaction phase: dispatched unavailability, conflict/capacity or cancellation remain conservatively `unknown` unless the owning result confirms success. Pure cancellation omits a caller-selected recovery reference because authorization may not have completed. The domain's protected diagnostics can distinguish actual rollback and commit uncertainty; diagnostic logs do not substitute for the durable successful Audit event.

Do not automatically repeat a mutation after a timeout, 503 or tool error, and do not replace its precondition with a newer revision. Current Account state and authorized Audit pages can inform investigation but do not prove which concurrent request caused a state change. This profile adds no client operation key, receipt lookup or before/after revision fields to Audit. Losing an entire response may also lose its generated request ID. Removal of the endpoint stops future admission and does not undo completed account changes or session revocation.

The normal pinned Notes consumer exercises actual PostgreSQL, TLS and official-SDK calls with current permissions, exact large revisions, atomic account/session/Audit behavior, failed-append rollback and separate Audit Query/Export. Those fixture credentials prove the resource-server seam; production OAuth and real direct/Gateway client interoperability remain separate product/#5 acceptance.
