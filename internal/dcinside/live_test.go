package dcinside

import (
	"context"
	"errors"
	"os"
	"testing"

	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/platform"
)

func TestLivePublicReadOnlyCollection(t *testing.T) {
	if os.Getenv("JACKPOT_DCINSIDE_LIVE") != "1" {
		t.Skip("explicit read-only network smoke")
	}
	transport := platform.NewHTTPTransport()
	defer transport.CloseIdleConnections()
	collector, err := NewCollector(transport, appclock.System{})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://gall.dcinside.com/mini/board/view/?id=compliment&no=72523", "https://gall.dcinside.com/board/view/?id=tree&no=1019123"} {
		snapshot, err := collector.Collect(context.Background(), raw, nil)
		if err != nil {
			var typed *Error
			if errors.As(err, &typed) {
				t.Fatalf("read-only %s page=%d", typed.Code, typed.Page)
			}
			t.Fatal("read-only unexpected failure")
		}
		if !snapshot.Complete || snapshot.Pages == 0 || snapshot.Article.Title == "" {
			t.Fatal("live complete invariant")
		}
		t.Logf("kind=%s complete=%t pages=%d accepted=%d deleted=%d unsupported=%d", snapshot.Article.Ref.Kind, snapshot.Complete, snapshot.Pages, len(snapshot.Comments), snapshot.DeletedComments, snapshot.UnsupportedComments)
	}
}
