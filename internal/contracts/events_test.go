package contracts_test

import (
	"encoding/json"
	"github.com/porkyx/jackpot/internal/contracts"
	"math"
	"strings"
	"testing"
)

func TestStateNoticeSuccessKindsRevisionBoundsAndOptionalOperationAreReadOnly(t *testing.T) {
	operation := contracts.OperationID("operation")
	for _, kind := range []contracts.EventEntityKind{contracts.DraftEntity, contracts.CollectionEntity} {
		for _, revision := range []contracts.Revision{0, 1, contracts.Revision(contracts.MaxSafeInteger)} {
			for _, id := range []*contracts.OperationID{nil, &operation} {
				notice := contracts.StateNotice{BackendSessionID: "session", EntityKind: kind, EntityID: "entity", Revision: revision, OperationID: id}
				before := notice
				if err := notice.Validate(); err != nil {
					t.Fatal(err)
				}
				if notice != before {
					t.Fatal("notice validation changed state")
				}
				raw, err := json.Marshal(notice)
				if err != nil {
					t.Fatal(err)
				}
				if id == nil && !strings.Contains(string(raw), `"operationId":null`) {
					t.Fatal("nullable operation became omitted")
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 5 {
					t.Fatalf("private fields or missing metadata: %s/%v", raw, err)
				}
			}
		}
	}
}
func TestStateNoticeRejectsIndependentMetadataFailures(t *testing.T) {
	empty := contracts.OperationID("")
	for _, field := range []string{"session", "entity", "kind empty", "kind unknown", "revision safe+1", "revision uintmax", "operation"} {
		t.Run(field, func(t *testing.T) {
			notice := contracts.StateNotice{BackendSessionID: "session", EntityKind: contracts.DraftEntity, EntityID: "entity"}
			switch field {
			case "session":
				notice.BackendSessionID = ""
			case "entity":
				notice.EntityID = ""
			case "kind empty":
				notice.EntityKind = ""
			case "kind unknown":
				notice.EntityKind = "unknown"
			case "revision safe+1":
				notice.Revision = contracts.Revision(contracts.MaxSafeInteger + 1)
			case "revision uintmax":
				notice.Revision = contracts.Revision(math.MaxUint64)
			case "operation":
				notice.OperationID = &empty
			}
			if err := notice.Validate(); err == nil {
				t.Fatal("corrupt notice accepted")
			}
		})
	}
}
