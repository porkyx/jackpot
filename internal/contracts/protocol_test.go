package contracts_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

func fixedNow() time.Time { return time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC) }
func responseHeader(t *testing.T) contracts.ResponseHeader {
	t.Helper()
	header, err := contracts.NewResponseHeader("session", fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	return header
}
func pointer[T any](value T) *T { return &value }

func TestResponseHeaderNormalizesKSTToUTC(t *testing.T) {
	now := fixedNow().In(time.FixedZone("KST", 9*3600))
	header, err := contracts.NewResponseHeader("session", now)
	if err != nil || !header.OccurredAt.Equal(now) || header.OccurredAt.Location() != time.UTC {
		t.Fatal(header, err)
	}
	encoded, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"occurredAt":"2026-10-06T05:00:00Z"`) {
		t.Fatal(string(encoded))
	}
}

func TestResponseHeaderRejectsEachInvalidField(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*contracts.ResponseHeader)
	}{
		{"zero protocol", func(h *contracts.ResponseHeader) { h.ProtocolVersion = 0 }},
		{"unknown protocol", func(h *contracts.ResponseHeader) { h.ProtocolVersion = 2 }},
		{"empty session", func(h *contracts.ResponseHeader) { h.BackendSessionID = "" }},
		{"zero time", func(h *contracts.ResponseHeader) { h.OccurredAt = time.Time{} }},
		{"non UTC time", func(h *contracts.ResponseHeader) { h.OccurredAt = fixedNow().In(time.FixedZone("KST", 32400)) }},
		{"unrepresentable year", func(h *contracts.ResponseHeader) { h.OccurredAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"year before RFC3339", func(h *contracts.ResponseHeader) { h.OccurredAt = time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC) }},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			h := responseHeader(t)
			tt.mutate(&h)
			if h.Validate() == nil {
				t.Fatal("invalid header accepted")
			}
		})
	}
	if _, err := contracts.NewResponseHeader("", fixedNow()); err == nil {
		t.Fatal("empty session accepted")
	}
}

func TestResponseHeaderAcceptsMinimumAndMaximumRepresentableNonzeroTime(t *testing.T) {
	for _, now := range []time.Time{time.Date(1, 1, 1, 0, 0, 0, 1, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)} {
		if _, err := contracts.NewResponseHeader("session", now); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnvelopeRejectsUnsupportedJSONData(t *testing.T) {
	fn := func() {}
	e := contracts.Envelope[func()]{ResponseHeader: responseHeader(t), OK: true, Data: &fn}
	if _, err := json.Marshal(e); err == nil {
		t.Fatal("unsupported JSON data accepted")
	}
}

func TestPublicFaultCodeValidationRejectsUnknown(t *testing.T) {
	for _, code := range []contracts.ErrorCode{contracts.InvalidInput, contracts.InvalidState, contracts.StaleRevision, contracts.StaleArticleContext, contracts.BackendSessionChanged, contracts.ProtocolError} {
		if err := code.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []contracts.ErrorCode{"", "unknown"} {
		assertCode(t, code.Validate(), contracts.InvalidState)
	}
}

func FuzzMutationHeaderJSON(f *testing.F) {
	for _, raw := range []string{`{}`, `null`, `{"protocolVersion":1,"backendSessionId":"session","operationId":"op","expectedRevision":0}`, `{"expectedRevision":-1}`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 16*1024 {
			return
		}
		var h contracts.MutationHeader
		if json.Unmarshal([]byte(raw), &h) != nil {
			return
		}
		if h.Validate() == nil {
			if h.ProtocolVersion != 1 || h.BackendSessionID == "" || h.OperationID == "" || h.ExpectedRevision == nil || uint64(*h.ExpectedRevision) > contracts.MaxSafeInteger {
				t.Fatal("invalid decoded header")
			}
		}
	})
}

func mutationHeader() contracts.MutationHeader {
	return contracts.MutationHeader{ProtocolVersion: 1, BackendSessionID: "session", OperationID: "op", ExpectedRevision: pointer(contracts.Revision(0))}
}

func TestMutationHeaderAcceptsRequiredZeroAndSafeMaximum(t *testing.T) {
	for _, rev := range []contracts.Revision{0, 1, contracts.Revision(contracts.MaxSafeInteger)} {
		h := mutationHeader()
		h.ExpectedRevision = &rev
		if err := h.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMutationHeaderRejectsEachInvalidField(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*contracts.MutationHeader)
	}{
		{"protocol", func(h *contracts.MutationHeader) { h.ProtocolVersion = 0 }},
		{"session", func(h *contracts.MutationHeader) { h.BackendSessionID = "" }},
		{"operation", func(h *contracts.MutationHeader) { h.OperationID = "" }},
		{"missing revision", func(h *contracts.MutationHeader) { h.ExpectedRevision = nil }},
		{"unsafe revision", func(h *contracts.MutationHeader) {
			h.ExpectedRevision = pointer(contracts.Revision(contracts.MaxSafeInteger + 1))
		}},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			h := mutationHeader()
			tt.mutate(&h)
			if h.Validate() == nil {
				t.Fatal("invalid mutation accepted")
			}
		})
	}
}

func TestMutationJSONDistinguishesZeroFromNullOrMissing(t *testing.T) {
	for _, rev := range []string{``, `,"expectedRevision":null`, `,"expectedRevision":0`, `,"expectedRevision":9007199254740991`, `,"expectedRevision":9007199254740992`, `,"expectedRevision":-1`, `,"expectedRevision":1.5`} {
		raw := `{"protocolVersion":1,"backendSessionId":"session","operationId":"op"` + rev + `}`
		var h contracts.MutationHeader
		err := json.Unmarshal([]byte(raw), &h)
		valid := rev == `,"expectedRevision":0` || rev == `,"expectedRevision":9007199254740991`
		if err == nil {
			err = h.Validate()
		}
		if (err == nil) != valid {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}

func TestDraftMutationRequiresIDAndGeneration(t *testing.T) {
	h := contracts.DraftMutationHeader{MutationHeader: mutationHeader(), DraftID: "draft", ArticleGeneration: pointer(contracts.ArticleGeneration(0))}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*contracts.DraftMutationHeader){
		func(h *contracts.DraftMutationHeader) { h.ProtocolVersion = 0 },
		func(h *contracts.DraftMutationHeader) { h.DraftID = "" },
		func(h *contracts.DraftMutationHeader) { h.ArticleGeneration = nil },
		func(h *contracts.DraftMutationHeader) {
			h.ArticleGeneration = pointer(contracts.ArticleGeneration(contracts.MaxSafeInteger + 1))
		},
	} {
		copy := h
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatal("invalid draft mutation accepted")
		}
	}
}

func successEnvelope(t *testing.T) contracts.Envelope[string] {
	return contracts.Envelope[string]{ResponseHeader: responseHeader(t), OK: true, Data: pointer("ready")}
}

func TestEnvelopeSuccessOmitsFailureAndOptionalMutationFields(t *testing.T) {
	encoded, err := json.Marshal(successEnvelope(t))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"code", "messageKey", "operationId", "revision"} {
		if _, ok := wire[field]; ok {
			t.Fatalf("unexpected %s", field)
		}
	}
	if string(wire["data"]) != `"ready"` {
		t.Fatal(string(encoded))
	}
}

func TestEnvelopeFailureOmitsData(t *testing.T) {
	envelope := contracts.Envelope[string]{ResponseHeader: responseHeader(t), OK: false, Code: contracts.InvalidInput, MessageKey: "InvalidInput"}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"data"`) {
		t.Fatal("failure has success data")
	}
}

func TestEnvelopeRejectsEveryInvalidDiscriminantAndMetadataBranch(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*contracts.Envelope[string])
	}{
		{"header", func(e *contracts.Envelope[string]) { e.ProtocolVersion = 2 }},
		{"empty operation", func(e *contracts.Envelope[string]) { e.OperationID = pointer(contracts.OperationID("")) }},
		{"unsafe revision", func(e *contracts.Envelope[string]) {
			e.Revision = pointer(contracts.Revision(contracts.MaxSafeInteger + 1))
		}},
		{"missing success data", func(e *contracts.Envelope[string]) { e.Data = nil }},
		{"success with failure code", func(e *contracts.Envelope[string]) { e.Code = contracts.InvalidInput }},
		{"success with failure message", func(e *contracts.Envelope[string]) { e.MessageKey = "InvalidInput" }},
		{"failure with data", func(e *contracts.Envelope[string]) {
			e.OK = false
			e.Code = contracts.InvalidInput
			e.MessageKey = "InvalidInput"
		}},
		{"failure missing code", func(e *contracts.Envelope[string]) { e.OK = false; e.Data = nil; e.MessageKey = "InvalidInput" }},
		{"failure unknown code", func(e *contracts.Envelope[string]) {
			e.OK = false
			e.Data = nil
			e.Code = "unknown"
			e.MessageKey = "unknown"
		}},
		{"failure mismatched message", func(e *contracts.Envelope[string]) {
			e.OK = false
			e.Data = nil
			e.Code = contracts.InvalidInput
			e.MessageKey = "secret"
		}},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			e := successEnvelope(t)
			tt.mutate(&e)
			before := e
			if _, err := json.Marshal(e); err == nil {
				t.Fatal("invalid envelope serialized")
			}
			if e != before {
				t.Fatal("serialization changed envelope")
			}
		})
	}
}

func TestEnvelopePreservesExplicitZeroMutationRevision(t *testing.T) {
	e := successEnvelope(t)
	e.Revision = pointer(contracts.Revision(0))
	e.OperationID = pointer(contracts.OperationID("op"))
	encoded, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"revision":0`) || !strings.Contains(string(encoded), `"operationId":"op"`) {
		t.Fatal(string(encoded))
	}
}

func TestBootstrapJSONContainsExplicitNullAndEmptyArrays(t *testing.T) {
	b := contracts.BootstrapData{BackendNow: fixedNow(), Theme: "light", PendingOperations: []contracts.OperationDescriptor{}, RecentResults: []contracts.ResultReference{}}
	encoded, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"activeDraft":null`, `"pendingCursor":null`, `"pendingOperations":[]`, `"recentResults":[]`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatal(string(encoded))
		}
	}
}
