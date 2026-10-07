package main

import (
	"bytes"
	"context"
	"encoding/json"
	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/dcinside"
	"strconv"
	"strings"
	"testing"
)

func TestLargeFixtureUsesProductionCollectorExactly2Pages200Comments100Identities(t *testing.T) {
	transport := &fixtureTransport{large: true}
	collector, err := dcinside.NewCollector(transport, appclock.System{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := collector.Collect(context.Background(), largeFixtureURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Article.Ref.CanonicalURL != largeFixtureURL || snapshot.Article.Ref.GalleryID != largeGalleryID || snapshot.Article.Ref.ArticleNo != largeArticleNo || len(largeGalleryID) != 64 || len(largeFixtureURL) != 119 {
		t.Fatal("long canonical article identity changed", snapshot.Article.Ref)
	}
	if !snapshot.Complete || snapshot.Pages != 2 || len(snapshot.Comments) != 200 || !strings.HasPrefix(snapshot.Article.Title, "로컬 댓글 추첨 한글") {
		t.Fatal("large completeness failed")
	}
	identities := map[string]int{}
	ids := map[string]bool{}
	for index, comment := range snapshot.Comments {
		if comment.ID != strconv.Itoa(index+1) || ids[comment.ID] || comment.ParticipantKind != "fixed" {
			t.Fatal("order/id/kind changed")
		}
		if index == 0 {
			if comment.Kind != "dccon" || comment.Text != "[디시콘]" || len(comment.MediaURLs) != 1 || comment.MediaURLs[0] != "https://dcimg5.dcinside.com/test-owned-image.png" {
				t.Fatal("owned dccon wire lost", comment)
			}
		} else if comment.Kind != "text" || len(comment.MediaURLs) != 0 {
			t.Fatal("unexpected media row", index)
		}
		ids[comment.ID] = true
		identities[comment.Identifier]++
	}
	if len(identities) != 100 || identities["stress-user-00000"] != 60 || identities["stress-user-00001"] != 1 || identities["stress-user-00059"] != 2 {
		t.Fatal("participant distribution changed")
	}
	if transport.calls.Load() != 3 || transport.closed.Load() != 3 {
		t.Fatal("body lifetime changed")
	}
}
func TestLargePageBytesAreDeterministicBoundedAndRejectOutOfRange(t *testing.T) {
	for _, page := range []int{0, -1, 3, 21} {
		if _, err := largeComments(page); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	for _, page := range []int{1, 2} {
		first, err := largeComments(page)
		if err != nil {
			t.Fatal(err)
		}
		second, err := largeComments(page)
		if err != nil || !bytes.Equal(first, second) || len(first) > 1024*1024 {
			t.Fatal("large deterministic/size failed")
		}
		var wire struct {
			Total int              `json:"total_cnt"`
			Rows  []map[string]any `json:"comments"`
			Nav   string           `json:"pagination"`
		}
		if err = json.Unmarshal(first, &wire); err != nil || wire.Total != 200 || len(wire.Rows) != 100 {
			t.Fatal("large wire bounds failed")
		}
		if page == 2 && wire.Nav != "<em>2</em>" {
			t.Fatal("terminal nav changed")
		}
	}
}

func TestLocal100CommentFixtureIsCompleteAndClosesEveryResponse(t *testing.T) {
	transport := &fixtureTransport{large: true, commentCount: 100}
	collector, err := dcinside.NewCollector(transport, appclock.System{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := collector.Collect(context.Background(), largeFixtureURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Complete || snapshot.Pages != 2 || len(snapshot.Comments) != 100 {
		t.Fatalf("incomplete local fixture: pages=%d comments=%d", snapshot.Pages, len(snapshot.Comments))
	}
	identities := map[string]int{}
	for index, comment := range snapshot.Comments {
		if comment.ID != strconv.Itoa(index+1) {
			t.Fatal("comment order changed", index, comment.ID)
		}
		identities[comment.Identifier]++
	}
	if len(identities) != 41 || identities["stress-user-00000"] != 60 || identities["stress-user-00040"] != 1 {
		t.Fatal("local identity distribution changed", identities)
	}
	if transport.calls.Load() != 3 || transport.closed.Load() != 3 {
		t.Fatal("response body was not released", transport.calls.Load(), transport.closed.Load())
	}
}

func TestLocalFixtureRejectsUnsupportedCountsWithoutOutput(t *testing.T) {
	for _, count := range []int{-1, 0, 99, 101, 199, 201} {
		if data, err := localComments(1, count); err == nil || data != nil {
			t.Fatalf("count %d accepted or produced output", count)
		}
	}
	for _, count := range []int{100, 200} {
		for _, page := range []int{1, 2} {
			data, err := localComments(page, count)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Total int              `json:"total_cnt"`
				Rows  []map[string]any `json:"comments"`
			}
			if err = json.Unmarshal(data, &wire); err != nil || wire.Total != count || len(wire.Rows) != count/2 {
				t.Fatalf("count=%d page=%d: invalid wire or error %v", count, page, err)
			}
		}
	}
}
