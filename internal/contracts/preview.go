package contracts

import (
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
)

const MaxParticipantPreviews = 3
const MaxPreviewUTF16 = 256

// PreviewText is display-only. Selection and frozen comments retain their
// complete original text. The ellipsis is included in the UTF-16 bound.
func PreviewText(text string) (string, error) {
	if !utf8.ValidString(text) {
		return "", NewFault(InvalidState)
	}
	units, ellipsisEnd := 0, 0
	iter := graphemes.FromString(text)
	for iter.Next() {
		for _, character := range iter.Value() {
			units++
			if character > 0xffff {
				units++
			}
		}
		if units > MaxPreviewUTF16 {
			return text[:ellipsisEnd] + "…", nil
		}
		end := iter.End()
		if units < MaxPreviewUTF16 {
			ellipsisEnd = end
		}
	}
	return text, nil
}

func PreviewTexts(texts []string) ([]string, error) {
	previews := make([]string, 0, min(len(texts), MaxParticipantPreviews))
	for _, text := range texts[:min(len(texts), MaxParticipantPreviews)] {
		preview, err := PreviewText(text)
		if err != nil {
			return nil, err
		}
		previews = append(previews, preview)
	}
	return previews, nil
}
