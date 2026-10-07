package roundlifecycle

import (
	"reflect"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestBadgePublicOutcomeCannotSelectZeroWeightParticipant(t *testing.T) {
	rules := contracts.BadgeRules{WeightingEnabled: true, Fixed: contracts.BadgeRule{Weight: 100}}
	input := RoundInput{BadgeRules: &rules, CandidateIDs: []contracts.ParticipantID{"positive", "zero"}, CandidateCategories: []contracts.BadgeCategory{contracts.BadgeFixed, contracts.BadgeMainManager}, Prizes: []Prize{{ID: "p", Count: 1}}, Mode: ImmediateMode}
	outcome := Outcome{Winners: []Winner{{ParticipantID: "zero", PrizeID: "p", Slot: 1}}, ExecutedAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), AlgorithmVersion: "weighted", AppVersion: "test"}
	if ValidateRoundInput(input) != nil || ValidateOutcome(input, outcome) == nil {
		t.Fatal("public outcome selected zero ratio")
	}
	outcome.Winners[0].ParticipantID = "positive"
	if err := ValidateOutcome(input, outcome); err != nil {
		t.Fatal(err)
	}
	rules.WeightingEnabled = false
	outcome.Winners[0].ParticipantID = "zero"
	if err := ValidateOutcome(input, outcome); err != nil {
		t.Fatal("disabled ratios changed public outcome", err)
	}
}

func TestBadgeSnapshotInputRejectsIndependentRulesCategoriesAndExcludedCandidates(t *testing.T) {
	rules := contracts.DefaultBadgeRules()
	collection := FrozenCollection{Filters: FilterSnapshot{BadgeRules: &rules}, Participants: []ParticipantSnapshot{{ID: "p", Kind: "fixed", BadgeCategory: contracts.BadgeMainManager, Included: true}}}
	input := RoundInput{BadgeRules: contracts.CloneBadgeRules(&rules), CandidateIDs: []contracts.ParticipantID{"p"}, CandidateCategories: []contracts.BadgeCategory{contracts.BadgeMainManager}}
	if err := ValidateBadgeInput(collection, input); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nil rules", "weight changed", "category changed", "category missing", "foreign candidate", "excluded", "zero weighted", "invalid badge", "identity mismatch"} {
		t.Run(mode, func(t *testing.T) {
			actualCollection := collection
			actualCollection.Filters.BadgeRules = contracts.CloneBadgeRules(&rules)
			actualCollection.Participants = append([]ParticipantSnapshot{}, collection.Participants...)
			actualInput := input
			actualInput.BadgeRules = contracts.CloneBadgeRules(&rules)
			actualInput.CandidateIDs = append([]contracts.ParticipantID{}, input.CandidateIDs...)
			actualInput.CandidateCategories = append([]contracts.BadgeCategory{}, input.CandidateCategories...)
			switch mode {
			case "nil rules":
				actualInput.BadgeRules = nil
			case "weight changed":
				actualInput.BadgeRules.MainManager.Weight = 99
			case "category changed":
				actualInput.CandidateCategories[0] = contracts.BadgeFixed
			case "category missing":
				actualInput.CandidateCategories = nil
			case "foreign candidate":
				actualInput.CandidateIDs[0] = "foreign"
			case "excluded":
				actualCollection.Participants[0].Included = false
			case "zero weighted":
				actualInput.BadgeRules.WeightingEnabled = true
				actualInput.BadgeRules.MainManager.Weight = 0
				actualCollection.Filters.BadgeRules = contracts.CloneBadgeRules(actualInput.BadgeRules)
			case "invalid badge":
				actualCollection.Participants[0].BadgeCategory = "invalid"
			case "identity mismatch":
				actualCollection.Participants[0].Kind = "anonymous"
			}
			if err := ValidateBadgeInput(actualCollection, actualInput); err == nil {
				t.Fatal("inconsistent snapshot accepted")
			}
		})
	}
	if !reflect.DeepEqual(input.BadgeRules, &rules) {
		t.Fatal("validation mutated rules")
	}
	legacy := FrozenCollection{Participants: []ParticipantSnapshot{{ID: "old", Included: true}}}
	if ValidateBadgeSnapshot(legacy) != nil || ValidateBadgeInput(legacy, RoundInput{}) != nil {
		t.Fatal("legacy nil rules changed")
	}
}
