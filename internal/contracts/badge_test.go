package contracts

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBadgeCategoriesIdentityCompatibilityAndLegacyDefaults(t *testing.T) {
	for _, kind := range []string{"fixed", "semi_fixed", "anonymous"} {
		actual, err := ResolveBadgeCategory("", kind)
		if err != nil || string(actual) != kind {
			t.Fatal(actual, err)
		}
	}
	for _, category := range BadgeCategories() {
		for _, kind := range []string{"fixed", "semi_fixed", "anonymous", "invalid"} {
			_, err := ResolveBadgeCategory(category, kind)
			valid := kind != "invalid" && ((category == BadgeAnonymous) == (kind == "anonymous")) && (category != BadgeFixed || kind == "fixed") && (category != BadgeSemiFixed || kind == "semi_fixed")
			if (err == nil) != valid {
				t.Fatal(category, kind, err)
			}
		}
		var legacy *BadgeRules
		rule, err := legacy.Rule(category)
		if err != nil || rule != (BadgeRule{Weight: 100}) {
			t.Fatal(rule, err)
		}
	}
	if _, err := ResolveBadgeCategory("", "invalid"); err == nil {
		t.Fatal("invalid identity accepted")
	}
	if _, err := (*BadgeRules)(nil).Rule("invalid"); err == nil {
		t.Fatal("unknown badge accepted")
	}
	if BadgeCategory("").Validate() == nil {
		t.Fatal("empty explicit badge accepted")
	}
}

func TestBadgeRulesBoundariesOwnershipAndLegacyJSON(t *testing.T) {
	defaults := DefaultBadgeRules()
	copy := CloneBadgeRules(&defaults)
	copy.Fixed.Weight = 0
	copy.Fixed.Excluded = true
	if defaults.Fixed != (BadgeRule{Weight: 100}) || CloneBadgeRules(nil) != nil {
		t.Fatal("rule alias/default")
	}
	for _, field := range []*BadgeRule{&defaults.Fixed, &defaults.SemiFixed, &defaults.MainManager, &defaults.SubManager, &defaults.NewAccount, &defaults.Anonymous} {
		field.Weight = 101
		if defaults.Validate() == nil {
			t.Fatal("weight 101 accepted")
		}
		field.Weight = 100
	}
	zero := BadgeRules{}
	if zero.Validate() != nil {
		t.Fatal("disabled zero ratios change uniform mode")
	}
	zero.WeightingEnabled = true
	if zero.Validate() == nil {
		t.Fatal("enabled all-zero accepted")
	}
	zero.Anonymous.Weight = 1
	if zero.Validate() != nil {
		t.Fatal("relative weights need not sum to 100")
	}
	var old FilterConfiguration
	if err := json.Unmarshal([]byte(`{"excludeAnonymous":true}`), &old); err != nil || old.BadgeRules != nil {
		t.Fatal(old, err)
	}
	raw, err := json.Marshal(old)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil || fields["badgeRules"] != nil {
		t.Fatal("legacy omitted rules changed", string(raw), err)
	}
	roundTrip := BadgeRules{}
	raw, _ = json.Marshal(defaults)
	if json.Unmarshal(raw, &roundTrip) != nil || !reflect.DeepEqual(defaults, roundTrip) {
		t.Fatal("rule round trip")
	}
}
