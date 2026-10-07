package roundlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestManualSnapshotStrictDecodePreservesBothBitsAndNeverChangesReceiverOnFailure(t *testing.T) {
	for _, included := range []bool{false, true} {
		for _, override := range []bool{false, true} {
			raw := []byte(fmt.Sprintf(`{"ManualIncluded":%t,"OverrideExcluded":%t}`, included, override))
			before := append([]byte(nil), raw...)
			actual := ManualStateSnapshot{}
			if err := actual.UnmarshalJSON(raw); err != nil || actual != (ManualStateSnapshot{included, override}) || !reflect.DeepEqual(raw, before) {
				t.Fatal("valid two-bit state was changed", actual, err)
			}
		}
	}
	for _, raw := range []string{"", `null`, `{}`, `{"ManualIncluded":true}`, `{"OverrideExcluded":true}`, `{"ManualIncluded":null,"OverrideExcluded":false}`, `{"ManualIncluded":false,"OverrideExcluded":null}`, `{"ManualIncluded":1,"OverrideExcluded":false}`, `{"ManualIncluded":false,"OverrideExcluded":"true"}`, `{"ManualIncluded":false,"OverrideExcluded":true,"Unknown":false}`, `{"ManualIncluded":false,"OverrideExcluded":true} false`, `{"ManualIncluded":false,"OverrideExcluded":true} {}`, `{"ManualIncluded":false,"OverrideExcluded":true} x`, `[]`, `{"ManualIncluded":false`} {
		t.Run(raw, func(t *testing.T) {
			initial := ManualStateSnapshot{true, false}
			actual := initial
			requireBoundaryFault(t, actual.UnmarshalJSON([]byte(raw)), contracts.InvalidState)
			if actual != initial {
				t.Fatal("failed decode partially changed manual state")
			}
		})
	}
	var absent *ManualStateSnapshot
	requireBoundaryFault(t, absent.UnmarshalJSON([]byte(`{"ManualIncluded":false,"OverrideExcluded":true}`)), contracts.InvalidState)
	maximum := make([]byte, 100<<20)
	for index := range maximum {
		maximum[index] = ' '
	}
	copy(maximum, []byte(`{"ManualIncluded":false,"OverrideExcluded":true}`))
	exact := ManualStateSnapshot{true, false}
	if err := exact.UnmarshalJSON(maximum); err != nil || exact != (ManualStateSnapshot{false, true}) {
		t.Fatal("exact persisted input bound rejected", err)
	}
	oversized := make([]byte, (100<<20)+1)
	actual := ManualStateSnapshot{true, true}
	requireBoundaryFault(t, actual.UnmarshalJSON(oversized), contracts.InvalidState)
	if actual != (ManualStateSnapshot{true, true}) {
		t.Fatal("oversized decode changed state")
	}
}

func TestManualSnapshotLegacyAbsentAndNullRemainUnknownWithoutInferringFromIncluded(t *testing.T) {
	for _, included := range []bool{false, true} {
		for _, suffix := range []string{"", `,"Manual":null`} {
			var participant ParticipantSnapshot
			raw := fmt.Sprintf(`{"ID":"legacy","Included":%t%s}`, included, suffix)
			if err := json.Unmarshal([]byte(raw), &participant); err != nil || participant.Manual != nil || participant.Included != included {
				t.Fatal("legacy state inferred manual bits", err, participant)
			}
			encoded, err := json.Marshal(participant)
			if err != nil || strings.Contains(string(encoded), `"Manual"`) {
				t.Fatal("legacy unknown acquired persisted false state", string(encoded), err)
			}
		}
	}
	participant := ParticipantSnapshot{ID: "legacy", Nickname: "name", PublicIdentifier: "id", Kind: "fixed", Included: true, Classification: "unclassified", Comments: []CommentSnapshot{}}
	raw, err := json.Marshal(participant)
	const golden = `{"ID":"legacy","Nickname":"name","PublicIdentifier":"id","Kind":"fixed","Included":true,"Classification":"unclassified","Reason":"","Comments":[]}`
	if err != nil || string(raw) != golden {
		t.Fatal("legacy JSON layout changed", string(raw), err)
	}
}

func TestManualSnapshotBothIndependentBitsAndLegacyUnknownChangeCreateFingerprint(t *testing.T) {
	seen := map[[32]byte]bool{}
	request := boundaryCreateRequest()
	legacy, err := canonicalCreate(request)
	if err != nil {
		t.Fatal(err)
	}
	seen[legacy] = true
	// Independent legacy oracle excludes the new field altogether.
	type oldParticipant struct {
		ID                               contracts.ParticipantID
		Nickname, PublicIdentifier, Kind string
		Included                         bool
		Classification, Reason           string
		Comments                         []CommentSnapshot
	}
	oldRows := []oldParticipant{}
	for _, row := range request.Collection.Participants {
		oldRows = append(oldRows, oldParticipant{row.ID, row.Nickname, row.PublicIdentifier, row.Kind, row.Included, row.Classification, row.Reason, row.Comments})
	}
	oldCollection := struct {
		ID                     contracts.CollectionID
		SourceDraftID          contracts.DraftID
		FinalizedDraftRevision contracts.Revision
		ArticleGeneration      contracts.ArticleGeneration
		Snapshot               contracts.SnapshotSummary
		Article                ArticleSnapshot
		Participants           []oldParticipant
		Filters                FilterSnapshot
	}{request.Collection.ID, request.Collection.SourceDraftID, request.Collection.FinalizedDraftRevision, request.Collection.ArticleGeneration, request.Collection.Snapshot, request.Collection.Article, oldRows, request.Collection.Filters}
	oldPayload := struct {
		Kind       string
		DraftID    contracts.DraftID
		Revision   contracts.Revision
		Generation contracts.ArticleGeneration
		Collection any
		Input      RoundInput
	}{"CreateCollection", request.Context.DraftID, request.Context.Revision, request.Context.ArticleGeneration, oldCollection, request.Input}
	raw, err := json.Marshal(oldPayload)
	if err != nil || sha256.Sum256(raw) != legacy {
		t.Fatal("legacy create hash changed", err)
	}
	for _, included := range []bool{false, true} {
		for _, override := range []bool{false, true} {
			request.Collection.Participants[0].Manual = &ManualStateSnapshot{included, override}
			digest, err := canonicalCreate(request)
			if err != nil || seen[digest] {
				t.Fatal("manual bits/unknown collapsed into same hash", included, override, err)
			}
			seen[digest] = true
		}
	}
}

func TestPrepareCreateOwnsManualPointersAndAdmissionRejectsEachIndependentChangedBit(t *testing.T) {
	for _, change := range []string{"none", "include", "override"} {
		t.Run(change, func(t *testing.T) {
			storage := boundaryPreparingStorage()
			var admitted *FrozenCollection
			originalAdmit := storage.admit
			storage.admit = func(ctx context.Context, request AdmitRoundRequest) (Admission, error) {
				admitted = request.NewCollection
				return originalAdmit(ctx, request)
			}
			service := boundaryService(t, storage)
			request := boundaryCreateRequest()
			shared := &ManualStateSnapshot{true, true}
			for index := range request.Collection.Participants {
				request.Collection.Participants[index].Manual = shared
			}
			prepared, err := service.PrepareCreate(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			if prepared.request.Collection.Participants[0].Manual == shared || prepared.request.Collection.Participants[1].Manual == shared || prepared.request.Collection.Participants[0].Manual == prepared.request.Collection.Participants[1].Manual {
				t.Fatal("JSON ownership copy retained manual aliases")
			}
			shared.ManualIncluded = false
			shared.OverrideExcluded = false
			actual := prepared.request.Collection

			actual.Participants = append([]ParticipantSnapshot{}, actual.Participants...)
			original := *actual.Participants[0].Manual
			actual.Participants[0].Manual = &original
			if change == "include" {
				actual.Participants[0].Manual.ManualIncluded = false
			}
			if change == "override" {
				actual.Participants[0].Manual.OverrideExcluded = false
			}
			_, err = service.AdmitCreate(context.Background(), prepared, actual)
			if change != "none" {
				requireBoundaryFault(t, err, contracts.StaleRevision)
				if storage.admissions.Load() != 0 || admitted != nil {
					t.Fatal("changed manual state reached durable admission")
				}
			} else if err != nil || admitted == nil || *admitted.Participants[0].Manual != (ManualStateSnapshot{true, true}) {
				t.Fatal("owned manual state was changed by caller", err)
			}

		})
	}
}

func FuzzManualSnapshotStrictDecodeNeverPanicsOrPartiallyMutates(f *testing.F) {
	for _, raw := range []string{`{"ManualIncluded":true,"OverrideExcluded":false}`, `{"ManualIncluded":false,"OverrideExcluded":true}`, `{}`, `null`, `{"ManualIncluded":null,"OverrideExcluded":true}`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<20 {
			t.Skip()
		}
		initial := ManualStateSnapshot{true, false}
		actual := initial
		if err := actual.UnmarshalJSON(raw); err != nil {
			if actual != initial {
				t.Fatal("failure changed manual state")
			}
			return
		}
		encoded, err := json.Marshal(actual)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ManualStateSnapshot
		if err = json.Unmarshal(encoded, &decoded); err != nil || decoded != actual {
			t.Fatal("valid manual state does not roundtrip", err)
		}
	})
}
