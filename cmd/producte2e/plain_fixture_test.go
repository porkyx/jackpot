package main

import (
	"context"
	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/dcinside"
	"strings"
	"testing"
)

func TestNormalFixtureMaliciousEntitiesRemainLiteralThroughProductionCollector(t *testing.T) {
	transport := new(fixtureTransport)
	collector, err := dcinside.NewCollector(transport, appclock.System{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := collector.Collect(context.Background(), fixtureURL, nil)
	if err != nil || !snapshot.Complete || len(snapshot.Comments) != 6 {
		t.Fatal(snapshot, err)
	}
	if snapshot.Article.Title != fixtureTitle || snapshot.Comments[0].Text != fixtureAuthorMemo || snapshot.Comments[0].Kind != "text" || len(snapshot.Comments[0].MediaURLs) != 0 {
		t.Fatal(snapshot.Article.Title, snapshot.Comments[0])
	}
	if !strings.Contains(snapshot.Article.Title, "<script>window.__jackpotInjected=true</script>") || !strings.Contains(snapshot.Comments[0].Text, "&nbsp;") {
		t.Fatal("literal payload changed")
	}
	if snapshot.Article.Author == nil || snapshot.Article.Author.Nickname != "작성자" || snapshot.Article.Author.Identifier != "writer" || snapshot.Comments[0].Identifier != "writer" || snapshot.Comments[5].Kind != "dccon" || snapshot.Comments[1].Text != "hello 참가합니다" {
		t.Fatal("normal identity/selection fixture changed", snapshot)
	}
	if strings.Contains(fixtureArticle(), "<script>") || strings.Contains(fixtureArticle(), "<img src=x") {
		t.Fatal("fixture introduced executable wire nodes")
	}
	if transport.calls.Load() != 2 || transport.closed.Load() != 2 {
		t.Fatal("fixture resources leaked")
	}
}
