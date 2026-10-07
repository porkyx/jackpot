package contracts

import "encoding/json"

const MaxQueryResponseBytes = 8 * 1024 * 1024

// ValidateQueryJSONSize measures the actual Go JSON encoding, including HTML
// escapes and UTF-8. A query either returns its complete page or an explicit
// protocol fault; no snapshot or comments are silently truncated.
func ValidateQueryJSONSize(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxQueryResponseBytes {
		return NewFault(ProtocolError)
	}
	return nil
}
