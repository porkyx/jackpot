package application

import (
	"reflect"
	"strings"
	"testing"

	"github.com/porkyx/jackpot/internal/selection"
)

func TestParticipantProjectionUsesThreeBoundedPreviewsAndPreservesFullSelectionComments(t *testing.T) {
	comments := []selection.Comment{{Text: strings.Repeat("😀", 129)}, {Kind: selection.Dccon}, {Kind: selection.Voice}, {Text: "fourth is detailed only"}}
	row := selection.Row{Participant: selection.Participant{ID: "p", Kind: selection.Fixed, Comments: comments}}
	original := append([]selection.Comment{}, comments...)
	actual, err := participantData(row)
	if err != nil || actual.CommentCount != 4 || !reflect.DeepEqual(actual.Previews, []string{strings.Repeat("😀", 127) + "…", "[디시콘]", "[보플]"}) {
		t.Fatal(actual, err)
	}
	if !reflect.DeepEqual(comments, original) || len(comments[0].Text) != len(strings.Repeat("😀", 129)) {
		t.Fatal("preview changed original selection input")
	}
}
func TestParticipantProjectionEmptyAndInvalidPreview(t *testing.T) {
	value, err := participantData(selection.Row{Participant: selection.Participant{Kind: selection.Fixed}})
	if err != nil || value.Previews == nil || len(value.Previews) != 0 {
		t.Fatal(value, err)
	}
	value, err = participantData(selection.Row{Participant: selection.Participant{Comments: []selection.Comment{{Text: string([]byte{0xff})}}}})
	if err == nil || value.Previews != nil {
		t.Fatal(value, err)
	}
}
