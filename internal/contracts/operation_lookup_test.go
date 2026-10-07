package contracts

import (
	"encoding/json"
	"testing"
)

func lookupObservation(state OperationState) OperationObservation {
	kind := "Rerun"
	collection := CollectionID("collection")
	round := RoundID("round")
	revision := Revision(1)
	code := StorageUnavailable
	value := OperationObservation{OperationID: "operation", State: state, Kind: &kind, CollectionID: &collection, RoundID: &round, Revision: &revision}
	if state == OperationUnknown {
		return OperationObservation{OperationID: "operation", State: state}
	}
	if state == OperationFailed {
		value.FailureCode = &code
	}
	return value
}
func TestOperationObservationAcceptsOnlyKnownStateAndConsistentMetadata(t *testing.T) {
	for _, state := range []OperationState{OperationUnknown, OperationPending, OperationSucceeded, OperationFailed} {
		t.Run(string(state), func(t *testing.T) {
			value := lookupObservation(state)
			if err := value.Validate(); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(value)
			if err := value.Validate(); err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(value)
			if string(before) != string(after) {
				t.Fatal("validation changed metadata")
			}
		})
	}
}
func TestOperationObservationRejectsIndependentInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		change func(*OperationObservation)
	}{
		{"empty_operation", func(v *OperationObservation) { v.OperationID = "" }},
		{"zero_state", func(v *OperationObservation) { v.State = "" }},
		{"unknown_state", func(v *OperationObservation) { v.State = "not-a-state" }},
		{"nil_kind", func(v *OperationObservation) { v.Kind = nil }},
		{"bad_kind", func(v *OperationObservation) { *v.Kind = "invalid" }},
		{"nil_collection", func(v *OperationObservation) { v.CollectionID = nil }},
		{"empty_collection", func(v *OperationObservation) { *v.CollectionID = "" }},
		{"nil_round", func(v *OperationObservation) { v.RoundID = nil }},
		{"empty_round", func(v *OperationObservation) { *v.RoundID = "" }},
		{"nil_revision", func(v *OperationObservation) { v.Revision = nil }},
		{"overflow_revision", func(v *OperationObservation) { *v.Revision = Revision(MaxSafeInteger + 1) }},
		{"pending_failure", func(v *OperationObservation) { code := StorageUnavailable; v.FailureCode = &code }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := lookupObservation(OperationPending)
			test.change(&value)
			if value.Validate() == nil {
				t.Fatal("invalid observation accepted")
			}
		})
	}
	for _, test := range []struct {
		name   string
		change func(*OperationObservation)
	}{
		{"kind", func(v *OperationObservation) { x := "Rerun"; v.Kind = &x }},
		{"collection", func(v *OperationObservation) { x := CollectionID("collection"); v.CollectionID = &x }},
		{"round", func(v *OperationObservation) { x := RoundID("round"); v.RoundID = &x }},
		{"revision", func(v *OperationObservation) { x := Revision(0); v.Revision = &x }},
		{"failure", func(v *OperationObservation) { x := StorageUnavailable; v.FailureCode = &x }},
	} {
		t.Run("unknown_with_"+test.name, func(t *testing.T) {
			value := lookupObservation(OperationUnknown)
			test.change(&value)
			if value.Validate() == nil {
				t.Fatal("unknown with target accepted")
			}
		})
	}
	for _, code := range []ErrorCode{"", "invalid"} {
		value := lookupObservation(OperationFailed)
		value.FailureCode = &code
		if value.Validate() == nil {
			t.Fatal("invalid failure code accepted")
		}
	}
	value := lookupObservation(OperationFailed)
	value.FailureCode = nil
	if value.Validate() == nil {
		t.Fatal("failed without code accepted")
	}
	value = lookupObservation(OperationSucceeded)
	code := StorageUnavailable
	value.FailureCode = &code
	if value.Validate() == nil {
		t.Fatal("success with failure accepted")
	}
}
func TestOperationLookupRequestAndRevisionBoundaries(t *testing.T) {
	if (OperationLookupRequest{}).Validate() == nil {
		t.Fatal("empty request accepted")
	}
	if (OperationLookupRequest{OperationID: "operation"}).Validate() != nil {
		t.Fatal("valid request rejected")
	}
	for _, revision := range []Revision{0, 1, Revision(MaxSafeInteger)} {
		value := lookupObservation(OperationPending)
		value.Revision = &revision
		if err := value.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if (OperationLookupResponse{}).Validate() == nil {
		t.Fatal("empty envelope accepted")
	}
}
