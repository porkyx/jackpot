package roundlifecycle

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/porkyx/jackpot/internal/contracts"
)

// An absent or null ParticipantSnapshot.Manual remains legacy-unknown. Once an
// object is present, neither independent bit may be guessed from a missing value.
func (manual *ManualStateSnapshot) UnmarshalJSON(raw []byte) error {
	if manual == nil || len(raw) > 100<<20 {
		return contracts.NewFault(contracts.InvalidState)
	}
	var wire struct {
		ManualIncluded   *bool
		OverrideExcluded *bool
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return contracts.NewFault(contracts.InvalidState)
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF || wire.ManualIncluded == nil || wire.OverrideExcluded == nil {
		return contracts.NewFault(contracts.InvalidState)
	}
	*manual = ManualStateSnapshot{ManualIncluded: *wire.ManualIncluded, OverrideExcluded: *wire.OverrideExcluded}
	return nil
}
