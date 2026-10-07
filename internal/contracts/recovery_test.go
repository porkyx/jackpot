package contracts_test

import (
	"math"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func validDescriptor() contracts.OperationDescriptor {
	return contracts.OperationDescriptor{OperationID: "op", Kind: "CreateCollection", CollectionID: "c", RoundID: "r", Status: contracts.OperationPending}
}

func TestPendingDescriptorKindsAndIndependentInvalidFields(t *testing.T) {
	for _, kind := range []string{"CreateCollection", "Rerun", "SetSchedule", "CancelSchedule", "RetryRound", "ExecuteDue"} {
		descriptor := validDescriptor()
		descriptor.Kind = kind
		if err := descriptor.ValidatePending(); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*contracts.OperationDescriptor)
	}{
		{"empty operation", func(d *contracts.OperationDescriptor) { d.OperationID = "" }},
		{"zero kind", func(d *contracts.OperationDescriptor) { d.Kind = "" }},
		{"unknown kind", func(d *contracts.OperationDescriptor) { d.Kind = "unknown" }},
		{"empty collection", func(d *contracts.OperationDescriptor) { d.CollectionID = "" }},
		{"empty round", func(d *contracts.OperationDescriptor) { d.RoundID = "" }},
		{"zero status", func(d *contracts.OperationDescriptor) { d.Status = "" }},
		{"unknown status", func(d *contracts.OperationDescriptor) { d.Status = contracts.OperationUnknown }},
		{"succeeded", func(d *contracts.OperationDescriptor) { d.Status = contracts.OperationSucceeded }},
		{"failed", func(d *contracts.OperationDescriptor) { d.Status = contracts.OperationFailed }},
		{"safe bound +1", func(d *contracts.OperationDescriptor) { d.Revision = contracts.Revision(contracts.MaxSafeInteger + 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			descriptor := validDescriptor()
			tc.change(&descriptor)
			before := descriptor
			if err := descriptor.ValidatePending(); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
			if descriptor != before {
				t.Fatal("validation mutated input")
			}
		})
	}
	descriptor := validDescriptor()
	descriptor.Revision = contracts.Revision(contracts.MaxSafeInteger)
	if err := descriptor.ValidatePending(); err != nil {
		t.Fatal(err)
	}
}

func TestPendingRequestLimitAndCursorBoundaries(t *testing.T) {
	empty := ""
	cursor := "opaque"
	for _, limit := range []uint32{0, 1, 64, 65, math.MaxUint32} {
		request := contracts.PendingOperationsRequest{Limit: &limit}
		err := request.Validate()
		if (err == nil) != (limit >= 1 && limit <= 64) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	limit := uint32(1)
	if err := (contracts.PendingOperationsRequest{}).Validate(); err == nil {
		t.Fatal("absent limit accepted")
	}
	if err := (contracts.PendingOperationsRequest{Limit: &limit, Cursor: &empty}).Validate(); err == nil {
		t.Fatal("empty cursor accepted")
	}
	if err := (contracts.PendingOperationsRequest{Limit: &limit, Cursor: &cursor}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPendingPageArrayCursorAndDescriptorInvariants(t *testing.T) {
	empty := ""
	cursor := "opaque"
	overflow := make([]contracts.OperationDescriptor, 65)
	for i := range overflow {
		overflow[i] = validDescriptor()
	}
	for _, page := range []contracts.PendingOperationsPage{
		{}, {Operations: overflow},
		{Operations: []contracts.OperationDescriptor{}, Cursor: &empty},
		{Operations: []contracts.OperationDescriptor{{}}},
	} {
		if err := page.Validate(); err == nil {
			t.Fatalf("invalid page accepted: %+v", page)
		}
	}
	descriptors := make([]contracts.OperationDescriptor, 64)
	for i := range descriptors {
		descriptors[i] = validDescriptor()
	}
	for _, page := range []contracts.PendingOperationsPage{{Operations: []contracts.OperationDescriptor{}}, {Operations: descriptors, Cursor: &cursor}} {
		if err := page.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPendingResponseRejectsInvalidEnvelope(t *testing.T) {
	if err := (contracts.PendingOperationsResponse{}).Validate(); err == nil {
		t.Fatal("invalid response envelope accepted")
	}
}
