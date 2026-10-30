# Installation planning

`github.com/AChWorks/achrix/installation` is independently versioned, standard-library-only operational tooling. It can run before an Application, database or administrator exists. It performs no host inspection, command execution, provisioning, migration, activation, network call or persistence.

The product supplies actual requirements, trusted observations and explicitly selected adapters. This package supplies one deterministic assessment/planning boundary and conservative re-entry decisions. The [real disposable-resource example](example_test.go) demonstrates private-file inspection, a product-owned effect, retained attempt metadata, a lost acknowledgement and verification without replay. It does not establish a production installer, Rixa/panel support, generic updater or Recovery implementation.

## Programmatic coverage boundary

| Consumer / owned operation | Direct Go contract | HTTP / MCP |
| --- | --- | --- |
| Product installer or operator tooling assesses requirements and retained progress | `Assess` | Not published; a remote assessment needs a real product operator-authentication and observation profile |
| Product executor checks a proposal against fresh evidence before an authorized effect | `Plan.Recheck` | Not published; the plan supplies no host, migration or activation authority |

This deliberate exception permits installation before an Application or account
exists. A product needing remote operator access must define its own trusted
identity, exact target, observations and supported effects before selecting an
adapter under the [coverage contract](../docs/architecture/contracts-and-interfaces.md#internal-and-external-programmatic-coverage).
There is no generic unauthenticated bootstrap endpoint or automatic export of
planning methods. Same-process tooling keeps direct access without a Bridge,
network hop or model.

## Inputs and binding

Public Go declarations own exact signatures.

| Contract | Responsibility |
| --- | --- |
| `Binding{ProfileID, OperationID, CandidateSHA256, PreconditionsSHA256}` | Product-selected exact operation/candidate/approved precondition identity. Digests are 64 lower-case hexadecimal characters encoding SHA-256; IDs are non-secret ASCII references of 1–128 bytes. |
| `Profile{Binding, Requirements}` | The product declares 1–1024 unique outcomes. Requirement order is the intended action order. Required outcomes block installation; context entries never block or create actions. |
| `Observation{RequirementID, State}` | Exactly one fresh observation per requirement. The product verifies the actual endpoint, resource, compatibility and intended state; existence of a tool, root or panel is not that proof. |
| `Support{RequirementID, AdapterID, Authorized}` | One explicitly selected supported adapter per required outcome where applicable. Products determine actual authority; this assertion neither grants permission nor exposes an executable callback. |
| `Progress{PlanID, Attempts}` | Product-owned retained attempt/verification evidence. Persist `Attempted` before effects and `Verified` only after actual postflight. An empty Progress is for a genuinely new operation only. |

Identifiers and digests must identify safe opaque references and non-secret artifact/configuration/target metadata. Never insert DSNs, paths, passwords, tokens, secret values or raw provider errors. A digest does not establish publisher authenticity, compatibility or permission. Products verify candidate authenticity and supported component/schema combinations independently and change their binding when the intended artifact, target or approved configuration/preconditions changes.

`Assess(profile, observations, supports, progress)` rejects malformed, missing, duplicate and unknown entries with `InputError` wrapping `ErrInput`; its `Field` and `Problem` identify the correction without echoing invalid values. A progress plan mismatch returns `ErrStale` with reconciliation guidance. Use `errors.Is` for category checks and `errors.As` for field details; do not compare full error strings. It returns no partially valid plan on those errors. No CPU, operating-system, package-manager, panel or product names are built into evaluation.

## Observation and plan meaning

| Observation | Meaning |
| --- | --- |
| `Satisfied` | The exact desired requirement was actually verified. |
| `Missing` | Currently absent or unsatisfied; this alone never proves a previous effect cannot arrive later. |
| `Unknown` | Unchecked, unavailable or inconclusive. Never counts as available or provisionable. |
| `Unsupported` | The product's selected supported profile cannot satisfy this outcome. |
| `NotApplied` | Stronger than absence: a trusted adapter fenced prior work and definitively proved that its effect did not occur **and cannot arrive later**. |

Outcomes retain the four installation categories `Available`, `Provisionable`, `OperatorRequired` and `Incompatible`, with stable reasons distinguishing missing adapter, authority, unknown evidence, reconciliation or lost verification. A provisionable outcome needs both an explicit adapter and its current authority.

Plan statuses distinguish:
- `Ready`: every required outcome is presently verified; this is the assessed requirement set, not a claim of whole-product deployment.
- `ChangesRequired`: a bounded action **proposal** exists after all other required preflight passes. The product must authorize and execute it separately.
- `OperatorActionRequired`: a requirement needs operator/provider input.
- `ReconciliationRequired`: an earlier attempt or drift requires investigation before mutation.
- `UnsupportedPlan`: a mandatory outcome is incompatible with the selected profile.

Blocked, unsupported or uncertain plans expose **zero Actions** even if another outcome could be provisioned. Context such as unavailable root, package management or a panel may be reported without blocking an otherwise satisfied provider-managed installation.

The immutable Plan snapshots input slices. `ID()` binds the exact profile, operation, candidate, preconditions, ordered requirement set and normalized adapter/authority declarations. Observations and progress deliberately do not change that ID because they advance during the same operation. The hash is a consistency precondition, not a permission or authentication token. Output JSON contains stable metadata only; human messages and localization belong to the product.

## Safe execution and re-entry

Before using a proposal, the product must:

1. authorize the exact candidate and target through its deployment boundary;
2. serialize conflicting operations with a real product/platform lock or fencing mechanism;
3. inspect fresh actual state and authority;
4. call `plan.Recheck(currentProfile, freshObservations, currentSupports, retainedProgress)` and use the returned plan, rejecting `ErrStale` instead of using cached actions;
5. durably record each attempt before its effect, execute only the selected supported operation, and verify afterward;
6. retain unknown outcomes and actionable failure state across interruption; never infer rollback from a timeout.

Recheck rejects changed binding, requirement/order or selected adapter/authority declarations. It reclassifies fresh observations and retained progress. It does not know whether a product's inspection is truthful/current, authenticate a candidate, grant authority or provide a concurrency lock.

An `Attempted` effect plus `Missing` or `Unknown` observation produces reconciliation-required with no replay action. `Satisfied` can verify that the intended effect exists. Only authoritative fenced `NotApplied` can permit a later action proposal when a previous attempt may have happened. A previously `Verified` resource that stops satisfying the requirement requires explicit recovery; it is not automatically recreated.

These semantics cover the observed difference between safely repeated migration and one-time administrator bootstrap without pretending that all commands are idempotent. Database commit acknowledgement, account identity, listener ownership, private paths, service persistence, TLS and panel operations remain adapter/product-specific. The package provides no automatic retry, arbitrary shell, privileged runner, journal/lease/workflow engine, backup format, down migration or production update mechanism.

## Version and validation

`Version()` reports this module's actual resolved tag/pseudo-version from Go build information. Source builds report `development`; replaced dependencies report `development-replacement`. The product retains exact artifact/source identities in addition to these labels. No Core runtime dependency is needed.

Run focused checks from this directory:

```bash
GOWORK=off go test -race -count=1 ./...
GOWORK=off go vet ./...
```

The repository's module-aware full validation and separately pinned Notes consumer own normal dependency-resolution/build identity and integrated proof. Published support remains limited to profiles that have their own executable evidence.
