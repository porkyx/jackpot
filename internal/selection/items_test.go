package selection

import (
	"reflect"
	"strings"
	"testing"
)

func TestItemCountsInitialBoundaryAndInactiveModePreservation(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, eligible := range []int{0, 1, 2, 10, 11} {
			for _, count := range []int{0, 1, 10, 11} {
				items := DefaultItems()
				items.SingleCount = count
				items.Multiple = []Item{{ID: "inactive", Name: strings.Repeat("x", 30), Count: 99}}
				prizes, err := ValidateItems(items, eligible, initial)
				valid := eligible >= 1 && (!initial || eligible >= 2) && count >= 1 && count <= 10 && count <= eligible
				if (err == nil) != valid {
					t.Fatalf("eligible%d count%d initial%v", eligible, count, initial)
				}
				if valid && (len(prizes) != 1 || prizes[0].Count != count || prizes[0].ID != "single-product" || prizes[0].Name != "상품") {
					t.Fatal("single prize")
				}
			}
		}
	}
	multiple := Items{Mode: Multiple, SingleCount: 99, Multiple: []Item{{ID: "same-name-a", Name: "같음", Count: 1}, {ID: "same-name-b", Name: "같음", Count: 1}}}
	if _, err := ValidateItems(multiple, 2, true); err != nil {
		t.Fatal("inactive single invalid count leaked")
	}
}
func TestMultipleItemsBoundsOrderDefaultNamesAndUTF16(t *testing.T) {
	for _, count := range []int{0, 1, 10, 11} {
		items := Items{Mode: Multiple}
		for index := 0; index < count; index++ {
			items.Multiple = append(items.Multiple, Item{ID: string(rune('a' + index)), Count: 1})
		}
		before := append([]Item(nil), items.Multiple...)
		prizes, err := ValidateItems(items, 10, true)
		if (err == nil) != (count >= 1 && count <= 10) {
			t.Fatal("item rows bound")
		}
		if err == nil {
			for index, item := range prizes {
				want := "상품"
				if count > 1 {
					want = "상품 " + string(rune('A'+index))
				}
				if item.Name != want || item.ID != before[index].ID {
					t.Fatal("order/default")
				}
			}
		}
		if !reflect.DeepEqual(before, items.Multiple) {
			t.Fatal("mutated item model")
		}
	}
	for _, name := range []string{strings.Repeat("한", 20), strings.Repeat("😀", 10), " "} {
		if _, err := ValidateItems(Items{Mode: Multiple, Multiple: []Item{{ID: "a", Name: name, Count: 1}}}, 2, true); err != nil {
			t.Fatal("name valid20")
		}
	}
	for _, item := range []Item{{ID: "", Count: 1}, {ID: "a", Count: 0}, {ID: "a", Count: 11}, {ID: "a", Name: strings.Repeat("한", 21), Count: 1}, {ID: "a", Name: strings.Repeat("😀", 11), Count: 1}, {ID: string([]byte{0xff}), Count: 1}, {ID: "a", Name: string([]byte{0xff}), Count: 1}} {
		if _, err := ValidateItems(Items{Mode: Multiple, Multiple: []Item{item}}, 10, true); err == nil {
			t.Fatal("invalid item")
		}
	}
	for _, items := range []Items{{Mode: "invalid"}, {Mode: Multiple, Multiple: []Item{{ID: "a", Count: 6}, {ID: "b", Count: 5}}}, {Mode: Multiple, Multiple: []Item{{ID: "a", Count: 1}, {ID: "a", Count: 1}}}} {
		if _, err := ValidateItems(items, 10, true); err == nil {
			t.Fatal("invalid item set")
		}
	}
}
func TestAddRemoveItemsRespectCapacityKeepInactiveInputsAndAreAtomic(t *testing.T) {
	base := DefaultItems()
	next, err := AddItem(base, "item-b", 2)
	if err != nil || next.Mode != Single || next.SingleCount != 1 || len(next.Multiple) != 2 || len(base.Multiple) != 1 {
		t.Fatal("add preserves mode/input")
	}
	if _, err := AddItem(base, "item-b", 1); err == nil {
		t.Fatal("insufficient capacity")
	}
	if _, err := AddItem(base, "item-a", 2); err == nil {
		t.Fatal("duplicate id")
	}
	if _, err := AddItem(base, "", 2); err == nil {
		t.Fatal("empty id")
	}
	full := base
	full.Multiple = make([]Item, 10)
	if _, err := AddItem(full, "new", 10); err == nil {
		t.Fatal("max rows")
	}
	removed, err := RemoveItem(next, "item-a")
	if err != nil || len(removed.Multiple) != 1 || removed.Multiple[0].ID != "item-b" || len(next.Multiple) != 2 {
		t.Fatal("delete preserves order/input")
	}
	if _, err := RemoveItem(base, "item-a"); err == nil {
		t.Fatal("last row")
	}
	if _, err := RemoveItem(next, "missing"); err == nil {
		t.Fatal("absent row")
	}
	if _, err := RemoveItem(Items{Multiple: []Item{{ID: "dup"}, {ID: "dup"}}}, "dup"); err == nil {
		t.Fatal("remove all would make empty")
	}
}
func TestMessageUTF16BoundsPreserveWhitespace(t *testing.T) {
	for _, message := range []string{"", " ", strings.Repeat("한", 20), strings.Repeat("😀", 10)} {
		if err := ValidateMessage(message); err != nil {
			t.Fatal("valid message")
		}
	}
	for _, message := range []string{strings.Repeat("한", 21), strings.Repeat("😀", 11), string([]byte{0xff})} {
		if err := ValidateMessage(message); err == nil {
			t.Fatal("invalid message")
		}
	}
}

func TestSinglePrizeRetainsConfiguredIdentityAndName(t *testing.T) {
	for _, name := range []string{"E2E 상품", "  공백 유지  ", strings.Repeat("😀", 10)} {
		items := Items{Mode: Single, SingleCount: 1, SingleID: "single-configured", SingleName: name}
		actual, err := ValidateItems(items, 2, true)
		if err != nil || len(actual) != 1 || actual[0].ID != "single-configured" || actual[0].Name != name {
			t.Fatalf("single configuration lost: %+v %v", actual, err)
		}
	}
}
func TestSinglePrizeEmptyNameUsesDefaultWithoutLosingConfiguredIdentity(t *testing.T) {
	actual, err := ValidateItems(Items{Mode: Single, SingleCount: 1, SingleID: "single-configured"}, 2, true)
	if err != nil || len(actual) != 1 || actual[0].ID != "single-configured" || actual[0].Name != "상품" {
		t.Fatalf("blank default lost identity: %+v %v", actual, err)
	}
}
func TestSinglePrizeNameRejectsOverTwentyUTF16AndInvalidUTF8(t *testing.T) {
	for _, name := range []string{strings.Repeat("a", 21), strings.Repeat("😀", 11), string([]byte{0xff})} {
		actual, err := ValidateItems(Items{Mode: Single, SingleCount: 1, SingleName: name}, 2, true)
		if err == nil || actual != nil {
			t.Fatalf("invalid name accepted: %q %+v %v", name, actual, err)
		}
	}
}
