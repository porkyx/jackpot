package contracts

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/graphemes"
)

func units(text string) int { return len(utf16.Encode([]rune(text))) }
func TestPreviewTextUTF16BoundAndWholeGraphemes(t *testing.T) {
	family := "👩‍👩‍👧‍👦"
	cases := []struct{ name, input, expected string }{
		{"empty", "", ""},
		{"zero literal", "0", "0"},
		{"256", strings.Repeat("가", 256), strings.Repeat("가", 256)},
		{"257", strings.Repeat("가", 257), strings.Repeat("가", 255) + "…"},
		{"emoji 256", strings.Repeat("😀", 128), strings.Repeat("😀", 128)},
		{"emoji overflow", strings.Repeat("😀", 129), strings.Repeat("😀", 127) + "…"},
		{"ZWJ before bound", strings.Repeat("a", 250) + family, strings.Repeat("a", 250) + "…"},
		{"ZWJ at exact bound", strings.Repeat("a", 245) + family, strings.Repeat("a", 245) + family},
		{"combining overflow", strings.Repeat("a", 255) + "e\u0301", strings.Repeat("a", 255) + "…"},
		{"combining exact", strings.Repeat("a", 254) + "e\u0301", strings.Repeat("a", 254) + "e\u0301"},
		{"Unicode16 mark cutoff", strings.Repeat("a", 253) + "a\U0001e5eexx", strings.Repeat("a", 253) + "…"},
		{"Unicode17 mark cutoff", strings.Repeat("a", 253) + "a\U0001e6e3xx", strings.Repeat("a", 253) + "…"},
		{"Unicode16 distinct letter", strings.Repeat("a", 253) + "\U0001e5e8xx", strings.Repeat("a", 253) + "\U0001e5e8…"},
		{"CRLF overflow", strings.Repeat("a", 255) + "\r\n", strings.Repeat("a", 255) + "…"},
		{"single giant grapheme", "a" + strings.Repeat("\u0301", 300), "…"},
		{"literal HTML", "<script>안전한 문자열</script>", "<script>안전한 문자열</script>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			actual, err := PreviewText(c.input)
			if err != nil || actual != c.expected || units(actual) > 256 {
				t.Fatalf("%q units%d err%v", actual, units(actual), err)
			}
		})
	}
}
func TestPreviewTextRejectsInvalidUTF8WithoutReplacement(t *testing.T) {
	if value, err := PreviewText(string([]byte{0xff})); err == nil || value != "" {
		t.Fatal(value, err)
	}
}
func TestPreviewTextsNilEmptyThreeFourAndOwnership(t *testing.T) {
	for _, texts := range [][]string{nil, {}, {"one"}, {"one", "two", "three"}, {"one", "two", "three", "four"}} {
		actual, err := PreviewTexts(texts)
		if err != nil || actual == nil || len(actual) != min(len(texts), 3) {
			t.Fatal(actual, err)
		}
		expected := append([]string{}, texts[:min(len(texts), 3)]...)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatal(actual, expected)
		}
		if len(texts) > 0 {
			texts[0] = "mutated"
			if actual[0] == "mutated" {
				t.Fatal("slice alias")
			}
		}
	}
}
func TestPreviewTextsFirstNthContinuousInvalidInputHasNoPartialSuccess(t *testing.T) {
	for _, nth := range []int{0, 1, 2, -1} {
		texts := []string{"one", "two", "three"}
		for i := range texts {
			if nth == -1 || i == nth {
				texts[i] = string([]byte{0xff})
			}
		}
		actual, err := PreviewTexts(texts)
		if err == nil || actual != nil {
			t.Fatal(nth, actual, err)
		}
	}
}
func FuzzPreviewTextBoundaries(f *testing.F) {
	for _, seed := range []string{"", strings.Repeat("😀", 129), "👩‍👩‍👧‍👦", strings.Repeat("a", 255) + "e\u0301", "\r\n", string([]byte{0xff})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1048576 {
			return
		}
		actual, err := PreviewText(input)
		if !utf8.ValidString(input) {
			if err == nil {
				t.Fatal("accepted invalid UTF8")
			}
			return
		}
		if err != nil || !utf8.ValidString(actual) || units(actual) > 256 {
			t.Fatal(actual, err)
		}
		if units(input) <= 256 {
			if actual != input {
				t.Fatal("unnecessary truncation")
			}
			return
		}
		if !strings.HasSuffix(actual, "…") {
			t.Fatal("missing omission marker")
		}
		prefix := strings.TrimSuffix(actual, "…")
		if !strings.HasPrefix(input, prefix) {
			t.Fatal("not a source prefix")
		}
		boundary := len(prefix) == 0
		iter := graphemes.FromString(input)
		for iter.Next() {
			end := iter.End()
			if end == len(prefix) {
				boundary = true
				break
			}
		}
		if !boundary {
			t.Fatal("split grapheme")
		}
	})
}
