// SPDX-License-Identifier: MPL-2.0

// Package installation evaluates product-declared installation requirements.
// It performs no discovery or mutation. Products own trusted observations,
// adapter authority, durable progress, serialization, effects and verification.
package installation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

var (
	ErrInput = errors.New("invalid installation input")
	ErrStale = errors.New("installation binding or plan changed")
)

// InputError identifies a malformed planner field without retaining its value.
// It preserves errors.Is(err, ErrInput); products may present Field and Problem
// directly to the trusted planner caller or map them into their transport.
type InputError struct{ Field, Problem string }

func (e *InputError) Error() string          { return fmt.Sprintf("%s: %s: %s", ErrInput, e.Field, e.Problem) }
func (e *InputError) Unwrap() error          { return ErrInput }
func inputError(field, problem string) error { return &InputError{Field: field, Problem: problem} }

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

const maxRequirements = 1024

// Binding identifies the exact operation, candidate and approved preconditions.
// Digests identify trusted non-secret artifact/configuration/target metadata.
// They do not establish publisher authenticity or grant execution authority.
type Binding struct {
	ProfileID           string
	OperationID         string
	CandidateSHA256     string
	PreconditionsSHA256 string
}

// Requirement order is the product's intended action order. Context entries
// (Required=false) never create actions or block required outcomes.
type Requirement struct {
	ID       string
	Required bool
}

type Profile struct {
	Binding      Binding
	Requirements []Requirement
}

type ObservedState string

const (
	Satisfied   ObservedState = "satisfied"
	Missing     ObservedState = "missing"
	Unknown     ObservedState = "unknown"
	Unsupported ObservedState = "unsupported"
	// NotApplied is stronger than absence: the adapter has fenced prior work and
	// definitively established that its effect did not occur and cannot arrive.
	NotApplied ObservedState = "not-applied"
)

type Observation struct {
	RequirementID string
	State         ObservedState
}

// Support is the selected adapter's current explicit support/authority assertion.
// Products must derive Authorized from their real deployment authority boundary;
// this declaration is not itself a permission or executable callback.
type Support struct {
	RequirementID string
	AdapterID     string
	Authorized    bool
}

type AttemptState string

const (
	Attempted AttemptState = "attempted"
	Verified  AttemptState = "verified"
)

type Attempt struct {
	RequirementID string
	State         AttemptState
}

// Progress is product-owned durable evidence bound to the original plan.
// Persist Attempted before a mutation, then Verified only after actual postflight.
// A new empty Progress is valid only when no operation has previously started.
type Progress struct {
	PlanID   string
	Attempts []Attempt
}

type OutcomeState string

const (
	Available        OutcomeState = "available"
	Provisionable    OutcomeState = "provisionable"
	OperatorRequired OutcomeState = "operator-required"
	Incompatible     OutcomeState = "unsupported"
)

type Reason string

const (
	ReasonVerified             Reason = "verified"
	ReasonMissing              Reason = "missing"
	ReasonUnverified           Reason = "unverified"
	ReasonUnsupported          Reason = "unsupported"
	ReasonAdapterRequired      Reason = "adapter-required"
	ReasonAuthorityRequired    Reason = "authority-required"
	ReasonReconcileRequired    Reason = "reconciliation-required"
	ReasonVerificationLost     Reason = "verification-lost"
	ReasonDefinitelyNotApplied Reason = "definitely-not-applied"
)

type Outcome struct {
	RequirementID string
	Required      bool
	State         OutcomeState
	Reason        Reason
	AdapterID     string
}

type Action struct {
	RequirementID string
	AdapterID     string
}

type PlanStatus string

const (
	Ready                  PlanStatus = "ready"
	ChangesRequired        PlanStatus = "changes-required"
	OperatorActionRequired PlanStatus = "operator-action-required"
	ReconciliationRequired PlanStatus = "reconciliation-required"
	UnsupportedPlan        PlanStatus = "unsupported"
)

// Plan is an immutable snapshot. Its ID binds the profile, candidate, operation,
// preconditions, requirement order and selected adapter/authority declarations.
// Observations/progress do not change its ID: those must advance during the same
// operation. The ID is a consistency precondition, never an authorization token.
type Plan struct {
	id       string
	profile  Profile
	support  []Support
	outcomes []Outcome
	actions  []Action
	status   PlanStatus
}

func (p *Plan) ID() string          { return p.id }
func (p *Plan) Binding() Binding    { return p.profile.Binding }
func (p *Plan) Status() PlanStatus  { return p.status }
func (p *Plan) Outcomes() []Outcome { return append([]Outcome(nil), p.outcomes...) }

// Actions returns only proposals allowed by an otherwise satisfied preflight.
// A blocked/unsupported/uncertain plan offers no partial executable action set.
func (p *Plan) Actions() []Action { return append([]Action(nil), p.actions...) }

// MarshalJSON exports only stable safe metadata, never profile secret values or
// raw inspection/provider errors. Caller-supplied identifiers must be non-secret.
func (p *Plan) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		ID       string
		Binding  Binding
		Status   PlanStatus
		Outcomes []Outcome
		Actions  []Action
	}{p.id, p.profile.Binding, p.status, p.outcomes, p.actions})
}

// Assess validates complete finite input, classifies required outcomes and makes
// a deterministic plan. Every declared requirement needs exactly one observation.
// Missing, duplicate, unknown and malformed entries fail closed. No host tools,
// root/panel/CPU detection, resource acquisition or effects occur here.
func Assess(profile Profile, observations []Observation, supports []Support, progress Progress) (*Plan, error) {
	for _, field := range []struct {
		name, value string
		valid       *regexp.Regexp
	}{
		{"binding.profile_id", profile.Binding.ProfileID, identifier},
		{"binding.operation_id", profile.Binding.OperationID, identifier},
		{"binding.candidate_sha256", profile.Binding.CandidateSHA256, digest},
		{"binding.preconditions_sha256", profile.Binding.PreconditionsSHA256, digest},
	} {
		if !field.valid.MatchString(field.value) {
			return nil, inputError(field.name, "invalid or missing identity; supply the declared identifier or lowercase SHA-256 digest")
		}
	}
	if len(profile.Requirements) == 0 || len(profile.Requirements) > maxRequirements {
		return nil, inputError("requirements", "declare between 1 and 1024 requirements")
	}
	requirements := make(map[string]Requirement, len(profile.Requirements))
	for i, r := range profile.Requirements {
		if !identifier.MatchString(r.ID) {
			return nil, inputError(fmt.Sprintf("requirements[%d].id", i), "use a valid requirement identifier")
		}
		if _, exists := requirements[r.ID]; exists {
			return nil, inputError(fmt.Sprintf("requirements[%d].id", i), "duplicate requirement; declare it once")
		}
		requirements[r.ID] = r
	}
	if len(observations) != len(requirements) {
		return nil, inputError("observations", "provide exactly one observation for each declared requirement")
	}
	if len(supports) > len(requirements) {
		return nil, inputError("supports", "support count exceeds declared requirements")
	}
	if len(progress.Attempts) > len(requirements) {
		return nil, inputError("progress.attempts", "attempt count exceeds declared requirements")
	}
	observed := make(map[string]ObservedState, len(observations))
	for i, o := range observations {
		if _, exists := requirements[o.RequirementID]; !exists {
			return nil, inputError(fmt.Sprintf("observations[%d].requirement_id", i), "requirement is not declared in this profile")
		}
		if _, duplicate := observed[o.RequirementID]; duplicate {
			return nil, inputError(fmt.Sprintf("observations[%d].requirement_id", i), "duplicate observation; provide one per requirement")
		}
		if !validObservation(o.State) {
			return nil, inputError(fmt.Sprintf("observations[%d].state", i), "use a supported observed state")
		}
		observed[o.RequirementID] = o.State
	}
	adapters := make(map[string]Support, len(supports))
	for i, s := range supports {
		r, exists := requirements[s.RequirementID]
		if !exists || !r.Required {
			return nil, inputError(fmt.Sprintf("supports[%d].requirement_id", i), "support must reference a declared required requirement")
		}
		if !identifier.MatchString(s.AdapterID) {
			return nil, inputError(fmt.Sprintf("supports[%d].adapter_id", i), "use a valid adapter identifier")
		}
		if _, duplicate := adapters[s.RequirementID]; duplicate {
			return nil, inputError(fmt.Sprintf("supports[%d].requirement_id", i), "duplicate support; select one adapter")
		}
		adapters[s.RequirementID] = s
	}
	p := &Plan{profile: Profile{Binding: profile.Binding, Requirements: append([]Requirement(nil), profile.Requirements...)}, status: Ready}
	// Profile order gives both action order and canonical support serialization.
	for _, r := range profile.Requirements {
		if s, exists := adapters[r.ID]; exists {
			p.support = append(p.support, s)
		}
	}
	encoded, _ := json.Marshal(struct {
		Profile Profile
		Support []Support
	}{p.profile, p.support})
	hash := sha256.Sum256(encoded)
	p.id = hex.EncodeToString(hash[:])
	attempts := make(map[string]AttemptState, len(progress.Attempts))
	if progress.PlanID == "" && len(progress.Attempts) != 0 {
		return nil, inputError("progress.plan_id", "a plan identity is required when attempts exist")
	}
	if progress.PlanID != "" && progress.PlanID != p.id {
		return nil, fmt.Errorf("%w: progress.plan_id: reconcile prior attempts against the current plan before effects", ErrStale)
	}
	for i, a := range progress.Attempts {
		r, exists := requirements[a.RequirementID]
		if !exists || !r.Required {
			return nil, inputError(fmt.Sprintf("progress.attempts[%d].requirement_id", i), "attempt must reference a declared required requirement")
		}
		if a.State != Attempted && a.State != Verified {
			return nil, inputError(fmt.Sprintf("progress.attempts[%d].state", i), "use attempted or verified from durable execution evidence")
		}
		if _, duplicate := attempts[a.RequirementID]; duplicate {
			return nil, inputError(fmt.Sprintf("progress.attempts[%d].requirement_id", i), "duplicate attempt; reconcile durable progress first")
		}
		attempts[a.RequirementID] = a.State
	}
	for _, r := range profile.Requirements {
		o := classify(r, observed[r.ID], adapters[r.ID], attempts[r.ID])
		p.outcomes = append(p.outcomes, o)
		if !r.Required {
			continue
		}
		switch {
		case o.State == Incompatible:
			p.status = UnsupportedPlan
		case o.Reason == ReasonReconcileRequired || o.Reason == ReasonVerificationLost:
			if p.status != UnsupportedPlan {
				p.status = ReconciliationRequired
			}
		case o.State == OperatorRequired:
			if p.status == Ready || p.status == ChangesRequired {
				p.status = OperatorActionRequired
			}
		case o.State == Provisionable:
			p.actions = append(p.actions, Action{r.ID, o.AdapterID})
			if p.status == Ready {
				p.status = ChangesRequired
			}
		}
	}
	if p.status != ChangesRequired {
		p.actions = nil
	}
	return p, nil
}

// Recheck must receive current product binding and newly inspected evidence,
// adapter authority and durable progress before effects. It rejects a stale
// candidate/configuration/operation or changed adapter declarations. Callers use
// the returned fresh plan, never a cached earlier action list.
func (p *Plan) Recheck(current Profile, observations []Observation, supports []Support, progress Progress) (*Plan, error) {
	if p == nil || current.Binding != p.profile.Binding {
		return nil, fmt.Errorf("%w: binding: assess the current candidate, preconditions and operation before effects", ErrStale)
	}
	next, err := Assess(current, observations, supports, progress)
	if err != nil {
		return nil, err
	}
	if next.id != p.id {
		return nil, fmt.Errorf("%w: requirements or support changed; assess a fresh plan before effects", ErrStale)
	}
	return next, nil
}

func validObservation(s ObservedState) bool {
	return s == Satisfied || s == Missing || s == Unknown || s == Unsupported || s == NotApplied
}

func classify(r Requirement, s ObservedState, adapter Support, attempt AttemptState) Outcome {
	o := Outcome{RequirementID: r.ID, Required: r.Required, State: OperatorRequired}
	switch {
	case s == Satisfied:
		o.State, o.Reason = Available, ReasonVerified
	case s == Unsupported:
		o.State, o.Reason = Incompatible, ReasonUnsupported
	case r.Required && attempt == Verified:
		o.Reason = ReasonVerificationLost
	case r.Required && attempt == Attempted && s != NotApplied:
		o.Reason = ReasonReconcileRequired
	case s == Unknown:
		o.Reason = ReasonUnverified
	case !r.Required:
		o.Reason = ReasonMissing
	case adapter.AdapterID == "":
		o.Reason = ReasonAdapterRequired
	case !adapter.Authorized:
		o.Reason = ReasonAuthorityRequired
	default:
		o.State, o.AdapterID = Provisionable, adapter.AdapterID
		o.Reason = ReasonMissing
		if s == NotApplied {
			o.Reason = ReasonDefinitelyNotApplied
		}
	}
	return o
}
