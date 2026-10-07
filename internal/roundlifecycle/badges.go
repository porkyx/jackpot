package roundlifecycle

import (
	"reflect"

	"github.com/porkyx/jackpot/internal/contracts"
)

func ParticipantDrawable(participant ParticipantSnapshot, rules *contracts.BadgeRules) (bool, error) {
	if !participant.Included {
		return false, nil
	}
	if rules == nil {
		return true, nil
	}
	category, err := contracts.ResolveBadgeCategory(participant.BadgeCategory, participant.Kind)
	if err != nil {
		return false, err
	}
	rule, err := rules.Rule(category)
	if err != nil {
		return false, err
	}
	return !rules.WeightingEnabled || rule.Weight > 0, nil
}

// ValidateBadgeSnapshot only adds constraints when the new optional metadata is
// present. Legacy snapshots retain their original uniform input and JSON bytes.
func ValidateBadgeSnapshot(collection FrozenCollection) error {
	if err := collection.Filters.BadgeRules.Validate(); err != nil {
		return err
	}
	for _, participant := range collection.Participants {
		if participant.BadgeCategory != "" || collection.Filters.BadgeRules != nil {
			if _, err := contracts.ResolveBadgeCategory(participant.BadgeCategory, participant.Kind); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateBadgeInput(collection FrozenCollection, input RoundInput) error {
	if err := ValidateBadgeSnapshot(collection); err != nil {
		return err
	}
	if !reflect.DeepEqual(collection.Filters.BadgeRules, input.BadgeRules) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	if input.BadgeRules == nil {
		return nil
	}
	byID := make(map[contracts.ParticipantID]ParticipantSnapshot, len(collection.Participants))
	for _, participant := range collection.Participants {
		byID[participant.ID] = participant
	}
	if len(input.CandidateIDs) != len(input.CandidateCategories) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	for index, id := range input.CandidateIDs {
		participant, exists := byID[id]
		if !exists {
			return contracts.NewFault(contracts.InvalidInput)
		}
		category, err := contracts.ResolveBadgeCategory(participant.BadgeCategory, participant.Kind)
		drawable, drawableErr := ParticipantDrawable(participant, input.BadgeRules)
		if err != nil || drawableErr != nil || !drawable || category != input.CandidateCategories[index] {
			return contracts.NewFault(contracts.InvalidInput)
		}
	}
	return nil
}
