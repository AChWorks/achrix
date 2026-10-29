// SPDX-License-Identifier: MPL-2.0
package installation

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func profile() Profile {
	return Profile{
		Binding:      Binding{ProfileID: "test-product", OperationID: "install-42", CandidateSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64)},
		Requirements: []Requirement{{ID: "database", Required: true}, {ID: "service", Required: true}, {ID: "host.root", Required: false}},
	}
}
func readyObservations() []Observation {
	return []Observation{{"database", Satisfied}, {"service", Satisfied}, {"host.root", Missing}}
}
func mustPlan(t *testing.T, p Profile, o []Observation, s []Support, progress Progress) *Plan {
	t.Helper()
	plan, err := Assess(p, o, s, progress)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestSatisfiedConstrainedEnvironmentNeedsNoHostAuthority(t *testing.T) {
	p := mustPlan(t, profile(), readyObservations(), nil, Progress{})
	if p.Status() != Ready || len(p.Actions()) != 0 {
		t.Fatalf("plan=%s actions=%v", p.Status(), p.Actions())
	}
	if p.Outcomes()[2].State != OperatorRequired {
		t.Fatal("nonblocking context was disguised as satisfied")
	}
}

func TestExplicitAdapterAndAuthorityRequiredBeforeAnyAction(t *testing.T) {
	observations := readyObservations()
	observations[0].State = Missing
	for _, tc := range []struct {
		name    string
		support []Support
		status  PlanStatus
		reason  Reason
	}{
		{"no adapter", nil, OperatorActionRequired, ReasonAdapterRequired},
		{"no authority", []Support{{"database", "selected-provider", false}}, OperatorActionRequired, ReasonAuthorityRequired},
		{"supported authorized provider", []Support{{"database", "selected-provider", true}}, ChangesRequired, ReasonMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := mustPlan(t, profile(), observations, tc.support, Progress{})
			if p.Status() != tc.status || p.Outcomes()[0].Reason != tc.reason {
				t.Fatalf("status=%s outcomes=%+v", p.Status(), p.Outcomes())
			}
			if tc.status == ChangesRequired {
				if !reflect.DeepEqual(p.Actions(), []Action{{"database", "selected-provider"}}) {
					t.Fatal(p.Actions())
				}
			} else if len(p.Actions()) != 0 {
				t.Fatal("blocked plan exposed partial actions")
			}
		})
	}
}

func TestUnsupportedOrUnknownOutcomeNeverBecomesProvisionable(t *testing.T) {
	for _, state := range []ObservedState{Unknown, Unsupported} {
		o := readyObservations()
		o[0].State = state
		p := mustPlan(t, profile(), o, []Support{{"database", "provider", true}}, Progress{})
		if p.Status() == Ready || p.Status() == ChangesRequired || len(p.Actions()) != 0 {
			t.Fatalf("%s unsafe plan: %+v", state, p)
		}
	}
}

func TestPreflightBlocksAllPartialMutations(t *testing.T) {
	o := readyObservations()
	o[0].State = Missing
	o[1].State = Unknown
	p := mustPlan(t, profile(), o, []Support{{"database", "provider", true}}, Progress{})
	if p.Status() != OperatorActionRequired || len(p.Actions()) != 0 {
		t.Fatal("unverified service allowed database mutation")
	}
}

func TestInputValidationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Profile, *[]Observation, *[]Support, *Progress)
	}{
		{"no requirements", func(p *Profile, _ *[]Observation, _ *[]Support, _ *Progress) { p.Requirements = nil }},
		{"invalid identity", func(p *Profile, _ *[]Observation, _ *[]Support, _ *Progress) {
			p.Binding.ProfileID = "postgres://private"
		}},
		{"invalid candidate", func(p *Profile, _ *[]Observation, _ *[]Support, _ *Progress) {
			p.Binding.CandidateSHA256 = "mutable-latest"
		}},
		{"invalid precondition", func(p *Profile, _ *[]Observation, _ *[]Support, _ *Progress) {
			p.Binding.PreconditionsSHA256 = "unverified"
		}},
		{"duplicate requirement", func(p *Profile, _ *[]Observation, _ *[]Support, _ *Progress) {
			p.Requirements[1].ID = p.Requirements[0].ID
		}},
		{"missing observation", func(_ *Profile, o *[]Observation, _ *[]Support, _ *Progress) { *o = (*o)[:2] }},
		{"duplicate observation", func(_ *Profile, o *[]Observation, _ *[]Support, _ *Progress) { (*o)[1] = (*o)[0] }},
		{"unknown observation", func(_ *Profile, o *[]Observation, _ *[]Support, _ *Progress) { (*o)[1].RequirementID = "unexpected" }},
		{"invalid observation state", func(_ *Profile, o *[]Observation, _ *[]Support, _ *Progress) { (*o)[1].State = "optimistic" }},
		{"unknown support", func(_ *Profile, _ *[]Observation, s *[]Support, _ *Progress) {
			*s = []Support{{"unexpected", "provider", true}}
		}},
		{"duplicate support", func(_ *Profile, _ *[]Observation, s *[]Support, _ *Progress) {
			*s = []Support{{"database", "provider", true}, {"database", "another", true}}
		}},
		{"invalid adapter", func(_ *Profile, _ *[]Observation, s *[]Support, _ *Progress) {
			*s = []Support{{"database", "shell;command", true}}
		}},
		{"context support", func(_ *Profile, _ *[]Observation, s *[]Support, _ *Progress) {
			*s = []Support{{"host.root", "root", true}}
		}},
		{"unbound attempted progress", func(_ *Profile, _ *[]Observation, _ *[]Support, p *Progress) {
			p.Attempts = []Attempt{{"database", Attempted}}
		}},
		{"too many requirements", func(p *Profile, _ *[]Observation, _ *[]Support, _ *Progress) {
			p.Requirements = make([]Requirement, maxRequirements+1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, o, s, progress := profile(), readyObservations(), []Support(nil), Progress{}
			tc.mutate(&p, &o, &s, &progress)
			if _, err := Assess(p, o, s, progress); !errors.Is(err, ErrInput) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestProgressIsBoundAndValidated(t *testing.T) {
	p := mustPlan(t, profile(), readyObservations(), nil, Progress{})
	for _, attempts := range [][]Attempt{
		{{"unexpected", Attempted}},
		{{"host.root", Attempted}},
		{{"database", "unknown"}},
		{{"database", Attempted}, {"database", Verified}},
	} {
		if _, err := Assess(profile(), readyObservations(), nil, Progress{PlanID: p.ID(), Attempts: attempts}); !errors.Is(err, ErrInput) {
			t.Fatalf("attempts=%v err=%v", attempts, err)
		}
	}
	if _, err := Assess(profile(), readyObservations(), nil, Progress{PlanID: strings.Repeat("f", 64)}); !errors.Is(err, ErrStale) {
		t.Fatalf("err=%v", err)
	}
}

func TestUnknownAcknowledgementCannotBeBlindlyReplayed(t *testing.T) {
	o := readyObservations()
	o[0].State = Missing
	support := []Support{{"database", "database-adapter", true}}
	p := mustPlan(t, profile(), o, support, Progress{})
	progress := Progress{PlanID: p.ID(), Attempts: []Attempt{{"database", Attempted}}}
	for _, state := range []ObservedState{Missing, Unknown} {
		o[0].State = state
		next, err := p.Recheck(profile(), o, support, progress)
		if err != nil || next.Status() != ReconciliationRequired || len(next.Actions()) != 0 {
			t.Fatalf("state=%s next=%+v err=%v", state, next, err)
		}
	}
	// Only a trusted adapter's definitive fence, not a missing lookup, permits
	// another action proposal. The library performs no automatic retry.
	o[0].State = NotApplied
	next, err := p.Recheck(profile(), o, support, progress)
	if err != nil || next.Status() != ChangesRequired || len(next.Actions()) != 1 {
		t.Fatalf("fenced next=%+v err=%v", next, err)
	}
	o[0].State = Satisfied
	next, err = p.Recheck(profile(), o, support, progress)
	if err != nil || next.Status() != Ready || len(next.Actions()) != 0 {
		t.Fatalf("verified next=%+v err=%v", next, err)
	}
}

func TestVerifiedResourceDriftRequiresExplicitRecovery(t *testing.T) {
	p := mustPlan(t, profile(), readyObservations(), []Support{{"database", "provider", true}}, Progress{})
	o := readyObservations()
	o[0].State = NotApplied
	next, err := p.Recheck(profile(), o, []Support{{"database", "provider", true}}, Progress{PlanID: p.ID(), Attempts: []Attempt{{"database", Verified}}})
	if err != nil || next.Status() != ReconciliationRequired || next.Outcomes()[0].Reason != ReasonVerificationLost || len(next.Actions()) != 0 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func TestBindingAndSupportChangesInvalidatePlan(t *testing.T) {
	support := []Support{{"database", "provider", true}}
	plan := mustPlan(t, profile(), readyObservations(), support, Progress{})
	for _, change := range []func(*Profile){
		func(p *Profile) { p.Binding.ProfileID = "another" },
		func(p *Profile) { p.Binding.OperationID = "another" },
		func(p *Profile) { p.Binding.CandidateSHA256 = strings.Repeat("c", 64) },
		func(p *Profile) { p.Binding.PreconditionsSHA256 = strings.Repeat("c", 64) },
		func(p *Profile) { p.Requirements[0], p.Requirements[1] = p.Requirements[1], p.Requirements[0] },
		func(p *Profile) { p.Requirements[0].Required = false },
	} {
		current := profile()
		change(&current)
		if _, err := plan.Recheck(current, readyObservations(), support, Progress{}); !errors.Is(err, ErrStale) && !errors.Is(err, ErrInput) {
			t.Fatalf("err=%v", err)
		}
	}
	for _, changed := range [][]Support{nil, {{"database", "another", true}}, {{"database", "provider", false}}} {
		if _, err := plan.Recheck(profile(), readyObservations(), changed, Progress{}); !errors.Is(err, ErrStale) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestImmutableCanonicalPlanAndProductOrder(t *testing.T) {
	p := profile()
	o := []Observation{{"service", Missing}, {"host.root", Unsupported}, {"database", Missing}}
	s := []Support{{"service", "process-provider", true}, {"database", "database-provider", true}}
	plan := mustPlan(t, p, o, s, Progress{})
	wantActions := []Action{{"database", "database-provider"}, {"service", "process-provider"}}
	if !reflect.DeepEqual(plan.Actions(), wantActions) {
		t.Fatal(plan.Actions())
	}
	o[0], o[2] = o[2], o[0]
	s[0], s[1] = s[1], s[0]
	reordered := mustPlan(t, p, o, s, Progress{})
	if reordered.ID() != plan.ID() {
		t.Fatal("observation/adapter input order changed identity")
	}
	p.Requirements[0].ID = "mutated"
	outcomes := plan.Outcomes()
	outcomes[0].RequirementID = "mutated"
	actions := plan.Actions()
	actions[0].AdapterID = "mutated"
	if !reflect.DeepEqual(plan.Actions(), wantActions) || plan.Outcomes()[0].RequirementID != "database" {
		t.Fatal("caller mutated plan")
	}
	encoded, err := json.Marshal(plan)
	if err != nil || strings.Contains(string(encoded), "mutated") {
		t.Fatalf("json=%s err=%v", encoded, err)
	}
}

func TestPublicErrorsAndOutputContainOnlyDeclaredMetadata(t *testing.T) {
	// There is deliberately no free-form diagnostic/credential field.
	p := mustPlan(t, profile(), readyObservations(), nil, Progress{})
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "password") || strings.Contains(string(b), "DSN") {
		t.Fatal(string(b))
	}
	if _, err := Assess(Profile{}, nil, nil, Progress{}); err.Error() != "invalid installation input" {
		t.Fatal(err)
	}
}
