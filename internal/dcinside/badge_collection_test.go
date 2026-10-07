package dcinside_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/porkyx/jackpot/internal/application"
	appclock "github.com/porkyx/jackpot/internal/clock"
	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/dcinside"
	"github.com/porkyx/jackpot/internal/selection"
)

func TestCollectorAdapterCarriesAllSixBadgeCategoriesWithoutChangingUIDOrIP(t *testing.T) {
	const canonical = "https://gall.dcinside.com/mini/board/view/?id=fixture&no=9008"
	const article = `<span class="title_subject">합성 배지 테스트</span><input id="e_s_n_o" value="synthetic-read-nonce">`
	files := []string{"fix_nik.gif", "nik.gif", "fix_managernik.gif", "sub_managernik.gif", "fix_newnik.gif", ""}
	want := []contracts.BadgeCategory{contracts.BadgeFixed, contracts.BadgeSemiFixed, contracts.BadgeMainManager, contracts.BadgeSubManager, contracts.BadgeNewAccount, contracts.BadgeAnonymous}
	wantKind := []selection.ParticipantKind{selection.Fixed, selection.SemiFixed, selection.Fixed, selection.SemiFixed, selection.Fixed, selection.Anonymous}
	rows := make([]map[string]any, len(files))
	for i, file := range files {
		id := strconv.Itoa(i + 1)
		nicktype, uid, ip := "00", "synthetic_"+id, ""
		if wantKind[i] == selection.Fixed {
			nicktype = "20"
		}
		if wantKind[i] == selection.Anonymous {
			uid, ip = "", "198.51"
		}
		rows[i] = map[string]any{"no": id, "parent": "9008", "name": "Participant_" + id, "user_id": uid, "ip": ip, "reg_date": "10.06 12:00:00", "nicktype": nicktype, "memo": "synthetic comment", "depth": 0, "c_no": 0, "del_yn": "N", "is_delete": "0", "gallog_icon": `<img src="https://nstatic.dcinside.com/dc/w/images/` + file + `">`}
	}
	wire, err := json.Marshal(map[string]any{"total_cnt": len(rows), "comments": rows, "pagination": "<em>1</em>", "allow_reply": 1})
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(wire)
	var calls, closed atomic.Int32
	transport := miniRegisteredTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		body := []byte(article)
		if request.Method == http.MethodPost {
			if _, err := io.Copy(io.Discard, request.Body); err != nil {
				t.Fatal(err)
			}
			if err := request.Body.Close(); err != nil {
				t.Fatal(err)
			}
			body = wire
		} else if request.Method != http.MethodGet {
			t.Fatal("unexpected mutation method")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: &miniRegisteredBody{Reader: bytes.NewReader(body), closed: &closed}}, nil
	})
	collector, err := dcinside.NewCollector(transport, appclock.System{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (application.DCCollectorAdapter{Collector: collector}).Collect(context.Background(), canonical, nil)
	if err != nil || snapshot.Pages != 1 || len(snapshot.Comments) != 6 {
		t.Fatal("badge collection incomplete", err)
	}
	for i, comment := range snapshot.Comments {
		id := strconv.Itoa(i + 1)
		identifier := "synthetic_" + id
		if i == 5 {
			identifier = "198.51"
		}
		if comment.BadgeCategory != want[i] || comment.ParticipantKind != wantKind[i] || comment.Identifier != identifier || comment.Nickname != "Participant_"+id || comment.PostedAt != nil {
			t.Fatal("adapter discarded category or changed identity/date", i)
		}
	}
	if calls.Load() != 2 || closed.Load() != 2 || !bytes.Equal(wire, before) {
		t.Fatal("extra request, response leak or fixture mutation")
	}
}
