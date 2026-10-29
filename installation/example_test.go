// SPDX-License-Identifier: MPL-2.0
package installation_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AChWorks/achrix/installation"
)

// This example is a single serialized product operation against a disposable
// private marker. A real adapter must supply its own cross-process lock/fence,
// durable atomic progress storage, artifact trust and target-specific checks.
func ExamplePlan_Recheck() {
	root, err := os.MkdirTemp("", "achrix-installation-example-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	resource := filepath.Join(root, "private-marker")
	journal := filepath.Join(root, "progress.json")
	snapshot := func(s string) string { d := sha256.Sum256([]byte(s)); return hex.EncodeToString(d[:]) }
	profile := installation.Profile{
		Binding: installation.Binding{
			ProfileID: "private-marker", OperationID: "install-42",
			CandidateSHA256:     snapshot("reviewed-example-artifact"),
			PreconditionsSHA256: snapshot("empty-private-marker-target:" + root),
		},
		Requirements: []installation.Requirement{{ID: "private.marker", Required: true}},
	}
	support := []installation.Support{{RequirementID: "private.marker", AdapterID: "product-marker", Authorized: true}}
	inspect := func() installation.Observation {
		body, readErr := os.ReadFile(resource)
		state := installation.Unknown
		switch {
		case errors.Is(readErr, os.ErrNotExist):
			state = installation.Missing
		case readErr == nil && string(body) == "install-42":
			state = installation.Satisfied
		case readErr == nil:
			state = installation.Unsupported // Preserve a resource this operation does not own.
		}
		return installation.Observation{RequirementID: "private.marker", State: state}
	}
	plan, err := installation.Assess(profile, []installation.Observation{inspect()}, support, installation.Progress{})
	if err != nil {
		panic(err)
	}
	fmt.Println(plan.Status())

	// The product durably records the attempt before its own effect. The private
	// journal carries only operation metadata, never credentials or raw errors.
	progress := installation.Progress{PlanID: plan.ID(), Attempts: []installation.Attempt{{RequirementID: "private.marker", State: installation.Attempted}}}
	data, err := json.Marshal(progress)
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(journal, data, 0o600); err != nil {
		panic(err)
	}
	// For this example, flush the private journal before attempting the effect.
	file, err := os.OpenFile(journal, os.O_RDWR, 0)
	if err != nil {
		panic(err)
	}
	if err = file.Sync(); err != nil {
		panic(err)
	}
	if err = file.Close(); err != nil {
		panic(err)
	}
	// Persist creation of the journal entry before crossing the effect boundary.
	directory, err := os.Open(root)
	if err != nil {
		panic(err)
	}
	if err = directory.Sync(); err != nil {
		panic(err)
	}
	if err = directory.Close(); err != nil {
		panic(err)
	}
	if len(plan.Actions()) != 1 {
		panic("preflight did not allow a proposal")
	}
	file, err = os.OpenFile(resource, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		panic(err)
	}
	if _, err = file.WriteString("install-42"); err != nil {
		panic(err)
	}
	if err = file.Sync(); err != nil {
		panic(err)
	}
	if err = file.Close(); err != nil {
		panic(err)
	}

	// Simulate losing the effect acknowledgement and returning to the operation
	// with progress loaded from disk. Unknown evidence cannot cause a replay.
	data, err = os.ReadFile(journal)
	if err != nil {
		panic(err)
	}
	progress = installation.Progress{}
	if err = json.Unmarshal(data, &progress); err != nil {
		panic(err)
	}
	uncertain := []installation.Observation{{RequirementID: "private.marker", State: installation.Unknown}}
	resumed, err := plan.Recheck(profile, uncertain, support, progress)
	if err != nil {
		panic(err)
	}
	fmt.Println(resumed.Status())
	if len(resumed.Actions()) != 0 {
		panic("unknown acknowledgement permitted a replay")
	}

	// Exact product-owned verification resolves the existing effect. No second
	// create is performed; a mere not-found read would still require a fence.
	verified, err := plan.Recheck(profile, []installation.Observation{inspect()}, support, progress)
	if err != nil {
		panic(err)
	}
	fmt.Println(verified.Status())
	fmt.Println("created once")
	// Output:
	// changes-required
	// reconciliation-required
	// ready
	// created once
}
