package draw

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestCategoryDrawUsesGroupSharesThenUniformPersonAndReweightsExhaustion(t *testing.T) {
	ids := []contracts.ParticipantID{"f1", "f2", "f3", "s1"}
	categories := []contracts.BadgeCategory{contracts.BadgeFixed, contracts.BadgeFixed, contracts.BadgeFixed, contracts.BadgeSemiFixed}
	rules := contracts.BadgeRules{WeightingEnabled: true, Fixed: contracts.BadgeRule{Weight: 70}, SemiFixed: contracts.BadgeRule{Weight: 30}}
	choices := []uint64{70, 0, 69, 2, 0, 0, 0, 0}
	wantBounds := []uint64{100, 1, 70, 3, 70, 2, 70, 1}
	var bounds []uint64
	entropy := entropyFunc(func(_ context.Context, bound uint64) (uint64, error) {
		bounds = append(bounds, bound)
		return choices[len(bounds)-1], nil
	})
	beforeIDs := append([]contracts.ParticipantID{}, ids...)
	beforeCategories := append([]contracts.BadgeCategory{}, categories...)
	beforeRules := rules
	actual, err := SampleCategories(context.Background(), ids, categories, []Prize{{ID: "p", Count: 4}}, &rules, entropy)
	if err != nil || !reflect.DeepEqual(bounds, wantBounds) {
		t.Fatal(actual, bounds, err)
	}
	wantIDs := []contracts.ParticipantID{"s1", "f3", "f1", "f2"}
	for index, winner := range actual {
		if winner.ParticipantID != wantIDs[index] || winner.PrizeID != "p" || winner.Slot != uint32(index+1) {
			t.Fatal(actual)
		}
	}
	if !reflect.DeepEqual(ids, beforeIDs) || !reflect.DeepEqual(categories, beforeCategories) || rules != beforeRules {
		t.Fatal("sampler mutated caller input")
	}
}

func TestCategoryDrawDisabledRulesPreserveLegacyOutputAndEntropyBounds(t *testing.T) {
	ids := []contracts.ParticipantID{"one", "two", "three"}
	prizes := []Prize{{ID: "p", Count: 2}}
	rules := contracts.BadgeRules{}
	categories := []contracts.BadgeCategory{contracts.BadgeFixed, contracts.BadgeSemiFixed, contracts.BadgeAnonymous}
	var bounds []uint64
	entropy := entropyFunc(func(_ context.Context, bound uint64) (uint64, error) {
		bounds = append(bounds, bound)
		return bound - 1, nil
	})
	legacy, err := Sample(context.Background(), ids, prizes, entropy)
	if err != nil {
		t.Fatal(err)
	}
	legacyBounds := append([]uint64{}, bounds...)
	bounds = nil
	actual, err := SampleCategories(context.Background(), ids, categories, prizes, &rules, entropy)
	if err != nil || !reflect.DeepEqual(legacy, actual) || !reflect.DeepEqual(bounds, legacyBounds) {
		t.Fatal(actual, bounds, err)
	}
	bounds = nil
	actual, err = SampleCategories(context.Background(), ids, nil, prizes, nil, entropy)
	if err != nil || !reflect.DeepEqual(legacy, actual) || !reflect.DeepEqual(bounds, legacyBounds) {
		t.Fatal(actual, bounds, err)
	}
}

func TestCategoryDrawThresholdIsGroupShareIndependentOfPopulation(t *testing.T) {
	ids := []contracts.ParticipantID{"f1", "f2", "f3", "s1"}
	categories := []contracts.BadgeCategory{contracts.BadgeFixed, contracts.BadgeFixed, contracts.BadgeFixed, contracts.BadgeSemiFixed}
	rules := contracts.BadgeRules{WeightingEnabled: true, Fixed: contracts.BadgeRule{Weight: 70}, SemiFixed: contracts.BadgeRule{Weight: 30}}
	for _, groupChoice := range []uint64{0, 69, 70, 99} {
		calls := 0
		actual, err := SampleCategories(context.Background(), ids, categories, []Prize{{ID: "p", Count: 1}}, &rules, entropyFunc(func(_ context.Context, bound uint64) (uint64, error) {
			calls++
			if calls == 1 {
				if bound != 100 {
					t.Fatal("population changed group share", bound)
				}
				return groupChoice, nil
			}
			wantBound := uint64(3)
			if groupChoice >= 70 {
				wantBound = 1
			}
			if bound != wantBound {
				t.Fatal("wrong inner population", bound)
			}
			return bound - 1, nil
		}))
		want := contracts.ParticipantID("f3")
		if groupChoice >= 70 {
			want = "s1"
		}
		if err != nil || calls != 2 || len(actual) != 1 || actual[0].ParticipantID != want {
			t.Fatal(actual, calls, err)
		}
	}
}

func TestCategoryDrawRejectsInvalidRulesBeforeEntropyAndNeverLeaksPartialResults(t *testing.T) {
	ids := []contracts.ParticipantID{"one", "two"}
	categories := []contracts.BadgeCategory{contracts.BadgeFixed, contracts.BadgeSemiFixed}
	prizes := []Prize{{ID: "p", Count: 2}}
	valid := contracts.BadgeRules{WeightingEnabled: true, Fixed: contracts.BadgeRule{Weight: 1}, SemiFixed: contracts.BadgeRule{Weight: 1}}
	for _, entry := range []struct {
		name       string
		rules      *contracts.BadgeRules
		categories []contracts.BadgeCategory
	}{
		{"nil rules with categories", nil, categories},
		{"category missing", &valid, categories[:1]},
		{"category extra", &valid, append(append([]contracts.BadgeCategory{}, categories...), contracts.BadgeFixed)},
		{"category invalid", &valid, []contracts.BadgeCategory{contracts.BadgeFixed, "invalid"}},
		{"all zero", &contracts.BadgeRules{WeightingEnabled: true}, categories},
		{"weight 101", &contracts.BadgeRules{WeightingEnabled: true, Fixed: contracts.BadgeRule{Weight: 101}}, categories},
		{"not enough positive", &contracts.BadgeRules{WeightingEnabled: true, Fixed: contracts.BadgeRule{Weight: 1}}, categories},
	} {
		t.Run(entry.name, func(t *testing.T) {
			calls := 0
			actual, err := SampleCategories(context.Background(), ids, entry.categories, prizes, entry.rules, entropyFunc(func(context.Context, uint64) (uint64, error) { calls++; return 0, nil }))
			if err == nil || actual != nil || calls != 0 {
				t.Fatal(actual, calls, err)
			}
		})
	}
	failure := errors.New("entropy failed")
	for _, failAt := range []int{1, 2, 3, 4} {
		for _, mode := range []string{"error", "bounds", "cancel"} {
			t.Run(mode+string(rune('0'+failAt)), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				actual, err := SampleCategories(ctx, ids, categories, prizes, &valid, entropyFunc(func(_ context.Context, bound uint64) (uint64, error) {
					calls++
					if calls == failAt {
						switch mode {
						case "error":
							return 0, failure
						case "bounds":
							return bound, nil
						case "cancel":
							cancel()
						}
					}
					return 0, nil
				}))
				if err == nil || actual != nil || calls != failAt {
					t.Fatal(actual, calls, err)
				}
			})
		}
	}
	var nilEntropy *scriptedCategoryEntropy
	if result, err := SampleCategories(context.Background(), ids, categories, prizes, &valid, nilEntropy); err == nil || result != nil {
		t.Fatal("typed nil entropy")
	}
	if result, err := SampleCategories(nil, ids, categories, prizes, &valid, entropyFunc(func(context.Context, uint64) (uint64, error) { return 0, nil })); err == nil || result != nil {
		t.Fatal("nil context")
	}
}

type scriptedCategoryEntropy struct{}

func (*scriptedCategoryEntropy) Intn(context.Context, uint64) (uint64, error) { return 0, nil }

func FuzzCategoryDrawNeverPanicsRepeatsOrLeaksPartialResults(f *testing.F) {
	f.Add(uint8(70), uint8(30), uint8(3), uint8(2), uint8(0))
	f.Fuzz(func(t *testing.T, fixed, semi, count, slots, choice uint8) {
		n := int(count%12) + 1
		ids := make([]contracts.ParticipantID, n)
		categories := make([]contracts.BadgeCategory, n)
		for index := range ids {
			ids[index] = contracts.ParticipantID(string(rune('a' + index)))
			categories[index] = contracts.BadgeFixed
			if index%2 == 1 {
				categories[index] = contracts.BadgeSemiFixed
			}
		}
		rules := contracts.BadgeRules{WeightingEnabled: true, Fixed: contracts.BadgeRule{Weight: uint32(fixed)}, SemiFixed: contracts.BadgeRule{Weight: uint32(semi)}}
		actual, err := SampleCategories(context.Background(), ids, categories, []Prize{{ID: "p", Count: uint32(slots % 11)}}, &rules, entropyFunc(func(_ context.Context, bound uint64) (uint64, error) { return uint64(choice), nil }))
		if err != nil {
			if actual != nil {
				t.Fatal("partial results")
			}
			return
		}
		seen := map[contracts.ParticipantID]bool{}
		for _, winner := range actual {
			if seen[winner.ParticipantID] {
				t.Fatal("repeat")
			}
			seen[winner.ParticipantID] = true
		}
	})
}
