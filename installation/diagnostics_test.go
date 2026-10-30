// SPDX-License-Identifier: MPL-2.0
package installation

import (
	"errors"
	"strings"
	"testing"
)

func TestPlannerInputExplainsExactFieldWithoutValues(t *testing.T) {
	cases := []struct {
		name        string
		modify      func(*Profile, []Observation)
		field, want string
	}{
		{"binding", func(p *Profile, _ []Observation) { p.Binding.OperationID = "PRIVATE SECRET" }, "binding.operation_id", "identity"},
		{"requirement", func(p *Profile, _ []Observation) { p.Requirements[1].ID = p.Requirements[0].ID }, "requirements[1].id", "duplicate"},
		{"observation", func(_ *Profile, o []Observation) { o[1].State = "PRIVATE SECRET" }, "observations[1].state", "supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, o := profile(), readyObservations()
			tc.modify(&p, o)
			plan, err := Assess(p, o, nil, Progress{})
			var detail *InputError
			if plan != nil || !errors.Is(err, ErrInput) || !errors.As(err, &detail) {
				t.Fatalf("missing typed field evidence: %v", err)
			}
			if detail.Field != tc.field || !strings.Contains(detail.Problem, tc.want) {
				t.Fatalf("wrong detail: %+v", detail)
			}
			if strings.Contains(err.Error(), "PRIVATE SECRET") {
				t.Fatal("input value leaked")
			}
		})
	}
}
