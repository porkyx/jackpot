package contracts

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func requireQuerySizeFault(t *testing.T, err error) {
	t.Helper()
	var fault Fault
	if !errors.As(err, &fault) || fault.Code != ProtocolError || fault.MessageKey != string(ProtocolError) {
		t.Fatalf("safe size fault: %v", err)
	}
}
func TestQueryJSONSizeExactUTF8Boundaries(t *testing.T) {
	const specBytes = 8 * 1024 * 1024
	if MaxQueryResponseBytes != specBytes {
		t.Fatalf("query byte cap differs from 8MiB specification: %d", MaxQueryResponseBytes)
	}
	type payload struct {
		Text string `json:"text"`
	}
	empty, err := json.Marshal(payload{})
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int{-1, 0, 1} {
		t.Run(map[int]string{-1: "below", 0: "exact", 1: "above"}[delta], func(t *testing.T) {
			value := payload{Text: strings.Repeat("x", MaxQueryResponseBytes-len(empty)+delta)}
			encoded, err := json.Marshal(value)
			if err != nil || len(encoded) != MaxQueryResponseBytes+delta {
				t.Fatalf("independent wire size: %d %v", len(encoded), err)
			}
			before := value.Text
			err = ValidateQueryJSONSize(value)
			if delta > 0 {
				requireQuerySizeFault(t, err)
			} else if err != nil {
				t.Fatal(err)
			}
			if value.Text != before {
				t.Fatal("size validation changed input")
			}
		})
	}
}
func TestQueryJSONSizeCountsHTMLEscapesAndMultibyteUTF8(t *testing.T) {
	type payload struct {
		Text string `json:"text"`
	}
	for _, item := range []struct {
		name, text string
		wireBytes  int
	}{{"html", "<>&", 18}, {"line-separators", "\u2028\u2029", 12}, {"korean", "한글", 6}, {"astral", "🙂", 4}} {
		t.Run(item.name, func(t *testing.T) {
			empty, _ := json.Marshal(payload{})
			encoded, _ := json.Marshal(payload{Text: item.text})
			if len(encoded)-len(empty) != item.wireBytes {
				t.Fatal("literal independent escaped length", string(encoded))
			}
			for _, delta := range []int{0, 1} {
				value := payload{Text: strings.Repeat("x", MaxQueryResponseBytes-len(empty)-item.wireBytes+delta) + item.text}
				encoded, _ = json.Marshal(value)
				if len(encoded) != MaxQueryResponseBytes+delta {
					t.Fatal("boundary wire length", len(encoded))
				}
				err := ValidateQueryJSONSize(value)
				if delta == 0 && err != nil {
					t.Fatal(err)
				}
				if delta == 1 {
					requireQuerySizeFault(t, err)
				}
			}
		})
	}
}
func TestQueryJSONSizeMarshalFailureDoesNotBecomeEmptySuccess(t *testing.T) {
	for _, value := range []any{math.NaN(), math.Inf(1), func() {}} {
		requireQuerySizeFault(t, ValidateQueryJSONSize(value))
	}
	for _, value := range []any{nil, "", 0, false, []string{}} {
		if err := ValidateQueryJSONSize(value); err != nil {
			t.Fatal(err)
		}
	}
}
