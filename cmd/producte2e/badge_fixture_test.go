package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/dcinside"
)

func TestBadgeFixtureKeepsSixCategoryIdentitiesAndClosesReadBodies(t *testing.T) {
	transport := &fixtureTransport{badges: true}
	collector, err := dcinside.NewCollector(transport, appclock.System{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := collector.Collect(context.Background(), fixtureURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Complete || snapshot.Pages != 1 || len(snapshot.Comments) != 12 {
		t.Fatal("incomplete badge fixture")
	}
	categories := []string{"fixed", "semi_fixed", "main_manager", "sub_manager", "new_account", "anonymous"}
	ids := map[string]bool{}
	for index, comment := range snapshot.Comments {
		if string(comment.BadgeCategory) != categories[index/2] || ids[comment.ID] {
			t.Fatal("badge category or stable comment ordering changed", index, comment)
		}
		ids[comment.ID] = true
		if index >= 10 {
			if comment.ParticipantKind != "anonymous" || !strings.HasPrefix(comment.Identifier, "192.0.2.") {
				t.Fatal("anonymous identity lost")
			}
		} else if comment.ParticipantKind == "anonymous" || !strings.HasPrefix(comment.Identifier, "badge-") {
			t.Fatal("registered identity lost")
		}
	}
	if transport.calls.Load() != 2 || transport.closed.Load() != 2 {
		t.Fatal("badge fixture leaked response bodies")
	}
}
func TestBadgeFixtureBytesAreDeterministicAndBounded(t *testing.T) {
	a, err := badgeComments()
	if err != nil {
		t.Fatal(err)
	}
	b, err := badgeComments()
	if err != nil || !bytes.Equal(a, b) || len(a) > 65536 {
		t.Fatal("non deterministic or oversized badge fixture")
	}
}
func TestMutuallyExclusiveBadgeAndLargeFixtureRejectsBeforeOpeningResources(t *testing.T) {
	err := run("", "", 0, true, 200, true)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatal("fixture conflict did not reject before resources", err)
	}
}
