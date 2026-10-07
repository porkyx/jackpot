// nativecloseseed creates only the explicit isolated fixture database used by
// verify-native-close.ps1. The production application is run unchanged afterward.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
	"github.com/porkyx/jackpot/internal/storage/sqlite"
)

func main() {
	directory := flag.String("data-root", "", "isolated child AppData directory")
	flag.Parse()
	if err := seed(*directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func seed(directory string) (err error) {
	if directory == "" || !filepath.IsAbs(directory) {
		return fmt.Errorf("absolute fixture directory required")
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(directory, "Jackpot", "jackpot.sqlite3"))
	if err != nil {
		return err
	}
	defer func() {
		closeErr := store.Close()
		if err == nil {
			err = closeErr
		}
	}()
	if _, err = store.Migrate(ctx); err != nil {
		return err
	}
	sequence := 0
	lifecycle, err := rl.NewService(rl.ServiceOptions{Storage: store, Session: "native-close-fixture", Clock: time.Now, NewID: func() string { sequence++; return fmt.Sprintf("close-%d", sequence) }})
	if err != nil {
		return err
	}
	defer func() {
		closeErr := lifecycle.Close(ctx)
		if err == nil {
			err = closeErr
		}
	}()
	now := time.Now().UTC()
	frozen := rl.FrozenCollection{SourceDraftID: "native-close-draft", FinalizedDraftRevision: 1, ArticleGeneration: 1, Snapshot: contracts.SnapshotSummary{SnapshotID: "native-close-snapshot", CollectedAt: now, Complete: true, Pages: 1, AcceptedComments: 2}, Article: rl.ArticleSnapshot{URL: "https://gall.dcinside.com/board/view/?id=example&no=1", Title: "예약 종료 안내 fixture", GalleryID: "example", Number: "1"}}
	for _, id := range []contracts.ParticipantID{"p-one", "p-two"} {
		frozen.Participants = append(frozen.Participants, rl.ParticipantSnapshot{ID: id, Nickname: string(id), PublicIdentifier: string(id), Kind: "fixed", Included: true, Classification: "included", Comments: []rl.CommentSnapshot{{ID: "comment-" + string(id), Kind: "text", Text: "fixture", PostedAt: &now}}})
	}
	round, err := lifecycle.CreateCollection(ctx, rl.CreateCollectionRequest{OperationID: "native-close-create", Context: contracts.DraftContext{BackendSessionID: "native-close-fixture", DraftID: "native-close-draft", Revision: 1, ArticleGeneration: 1}, Collection: frozen, Input: rl.RoundInput{CandidateIDs: []contracts.ParticipantID{"p-one", "p-two"}, Prizes: []rl.Prize{{ID: "prize", Name: "상품", Count: 1}}, Mode: rl.ReservationMode}})
	if err != nil {
		return err
	}
	scheduled := now.Add(time.Hour).Truncate(time.Minute)
	_, err = lifecycle.SetSchedule(ctx, rl.SetScheduleRequest{OperationID: "native-close-schedule", Context: contracts.RoundContext{CollectionContext: contracts.CollectionContext{BackendSessionID: "native-close-fixture", CollectionID: round.CollectionID, Revision: round.Revision}, RoundID: round.ID, Version: round.Version}, ScheduledAt: scheduled, Timezone: "Asia/Seoul"})
	if err != nil {
		return err
	}
	_, total, err := store.ListCollectionPage(ctx, 0, 1, "", "scheduled")
	if err != nil {
		return err
	}
	if total != 1 {
		return fmt.Errorf("fixture reservation count mismatch")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"scheduled": total, "collectionId": round.CollectionID, "scheduledAt": scheduled})
}
