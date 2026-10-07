package selection

import (
	"reflect"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestBadgeMetadataMergesRolesWithoutChangingIdentityOrDuplicateComments(t *testing.T) {
	for _, first := range []ParticipantKind{Fixed, SemiFixed} {
		second := Fixed
		if first == Fixed {
			second = SemiFixed
		}
		a := input("one", "same", "uid", first, "one")
		a.BadgeCategory = contracts.BadgeCategory(first)
		b := input("two", "same", "uid", second, "two")
		b.BadgeCategory = contracts.BadgeCategory(second)
		base := participants(t, a, b)
		if len(base) != 1 || base[0].Kind != first || base[0].BadgeCategory != a.BadgeCategory || base[0].ID != ParticipantID(base[0].Key) {
			t.Fatal("identity changed", base)
		}
		dup := a
		dup.BadgeCategory = contracts.BadgeMainManager
		p := participants(t, a, b, dup)
		if !reflect.DeepEqual(base, p) {
			t.Fatal("duplicate comment changed role")
		}
		for _, categories := range [][]contracts.BadgeCategory{{contracts.BadgeNewAccount, contracts.BadgeSubManager, contracts.BadgeMainManager}, {contracts.BadgeMainManager, contracts.BadgeSubManager, contracts.BadgeNewAccount}} {
			values := []InputComment{a, b}
			for index, category := range categories {
				c := input(string(rune('a'+index)), "same", "uid", first, "role")
				c.BadgeCategory = category
				values = append(values, c)
			}
			p := participants(t, values...)
			if p[0].Kind != first || p[0].ID != base[0].ID || p[0].BadgeCategory != contracts.BadgeMainManager || len(p[0].Comments) != 5 {
				t.Fatal(p)
			}
		}
	}
	bad := input("bad", "bad", "ip", Anonymous, "bad")
	bad.BadgeCategory = contracts.BadgeMainManager
	if _, err := BuildParticipants([]InputComment{bad}); err == nil {
		t.Fatal("anonymous role accepted")
	}
}

func TestBadgeExclusionsManualOverrideAndWeightsOffPreserveClassification(t *testing.T) {
	a := input("manager", "manager", "uid", Fixed, "keyword")
	a.BadgeCategory = contracts.BadgeMainManager
	p := participants(t, a, input("anonymous", "anonymous", "ip", Anonymous, "keyword"))
	rules := contracts.DefaultBadgeRules()
	rules.MainManager.Excluded = true
	filters := Filters{BadgeRules: &rules, IncludeKeywords: []string{"keyword"}, ExcludeAnonymous: true}
	result := selection(t, p, filters, &p[0].Key)
	if result.Rows[0].Classification.Reason != BadgeCategoryReason || result.Rows[1].Classification.Reason != AnonymousReason || result.Included != 0 {
		t.Fatal(result)
	}
	changed, err := ToggleManual(p, p[0].ID, filters, nil)
	if err != nil || !selection(t, changed, filters, nil).Rows[0].Classification.Included || p[0].Manual.OverrideExcluded {
		t.Fatal("manual override/alias", changed, err)
	}
	rules.MainManager.Excluded = false
	rules.MainManager.Weight = 0
	if selection(t, p, filters, nil).Included != 1 {
		t.Fatal("weights off changed individual inclusion")
	}
	checked, err := ValidateFilters(filters)
	if err != nil {
		t.Fatal(err)
	}
	checked.BadgeRules.MainManager.Excluded = true
	if rules.MainManager.Excluded {
		t.Fatal("filter rule alias")
	}
	rules.Fixed.Weight = 101
	if _, err = Evaluate(p, filters, nil); err == nil {
		t.Fatal("bad ratio accepted")
	}
}
