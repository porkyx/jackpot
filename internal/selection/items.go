package selection

import "unicode/utf8"

func DefaultItems() Items {
	return Items{Mode: Single, SingleCount: 1, Multiple: []Item{{ID: "item-a", Count: 1}}}
}
func ValidateMessage(message string) error {
	if !utf8.ValidString(message) || UTF16Length(message) > 20 {
		return invalid()
	}
	return nil
}
func ValidateItems(items Items, eligible int, initial bool) ([]Item, error) {
	if eligible < 1 || (initial && eligible < 2) {
		return nil, invalid()
	}
	var selected []Item
	switch items.Mode {
	case Single:
		id := items.SingleID
		if id == "" {
			id = "single-product"
		}
		selected = []Item{{ID: id, Name: items.SingleName, Count: items.SingleCount}}
	case Multiple:
		selected = append([]Item{}, items.Multiple...)
	default:
		return nil, invalid()
	}
	if len(selected) < 1 || len(selected) > 10 {
		return nil, invalid()
	}
	total := 0
	seen := make(map[string]struct{}, len(selected))
	for index := range selected {
		item := &selected[index]
		if item.ID == "" || !utf8.ValidString(item.ID) || !utf8.ValidString(item.Name) || UTF16Length(item.Name) > 20 || item.Count < 1 || item.Count > 10 {
			return nil, invalid()
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, invalid()
		}
		seen[item.ID] = struct{}{}
		total += item.Count
		if item.Name == "" {
			if len(selected) == 1 {
				item.Name = "상품"
			} else {
				item.Name = "상품 " + string(rune('A'+index))
			}
		}
	}
	if total > 10 || total > eligible {
		return nil, invalid()
	}
	return selected, nil
}
func AddItem(items Items, id string, eligible int) (Items, error) {
	if id == "" || len(items.Multiple) >= 10 {
		return Items{}, invalid()
	}
	next := items
	next.Mode = Multiple
	next.Multiple = append(append([]Item{}, items.Multiple...), Item{ID: id, Count: 1})
	if _, err := ValidateItems(next, eligible, false); err != nil {
		return Items{}, err
	}
	next.Mode = items.Mode
	return next, nil
}
func RemoveItem(items Items, id string) (Items, error) {
	if len(items.Multiple) <= 1 {
		return Items{}, invalid()
	}
	next := items
	next.Multiple = []Item{}
	found := false
	for _, item := range items.Multiple {
		if item.ID == id {
			found = true
		} else {
			next.Multiple = append(next.Multiple, item)
		}
	}
	if !found || len(next.Multiple) == 0 {
		return Items{}, invalid()
	}
	return next, nil
}
