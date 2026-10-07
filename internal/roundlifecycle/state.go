package roundlifecycle

import (
	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/draw"
)

const ImmediateMode = "immediate"
const ReservationMode = "reservation"

func drawPrizes(prizes []Prize) []draw.Prize {
	result := make([]draw.Prize, len(prizes))
	for index, prize := range prizes {
		result[index] = draw.Prize{ID: string(prize.ID), Name: prize.Name, Count: prize.Count}
	}
	return result
}
func ValidateRoundInput(input RoundInput) error {
	length, valid := draw.UTF16Length(input.Message)
	if !valid || length > 20 || (input.Mode != ImmediateMode && input.Mode != ReservationMode) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return draw.ValidateCategories(input.CandidateIDs, input.CandidateCategories, drawPrizes(input.Prizes), input.BadgeRules)
}
func ValidateOutcome(input RoundInput, outcome Outcome) error {
	if ValidateRoundInput(input) != nil || outcome.ExecutedAt.IsZero() || outcome.AlgorithmVersion == "" || outcome.AppVersion == "" {
		return contracts.NewFault(contracts.InvalidState)
	}
	if _, err := contracts.NewResponseHeader("outcome", outcome.ExecutedAt); err != nil {
		return contracts.NewFault(contracts.InvalidState)
	}
	total := uint32(0)
	prizes := make(map[PrizeID]uint32, len(input.Prizes))
	for _, prize := range input.Prizes {
		total += prize.Count
		prizes[prize.ID] = prize.Count
	}
	if len(outcome.Winners) != int(total) {
		return contracts.NewFault(contracts.InvalidState)
	}
	eligible := make(map[contracts.ParticipantID]bool, len(input.CandidateIDs))
	for index, id := range input.CandidateIDs {
		if input.BadgeRules != nil && input.BadgeRules.WeightingEnabled {
			rule, err := input.BadgeRules.Rule(input.CandidateCategories[index])
			if err != nil || rule.Weight == 0 {
				continue
			}
		}
		eligible[id] = true
	}
	seen := make(map[contracts.ParticipantID]bool, len(outcome.Winners))
	slots := make(map[PrizeID]map[uint32]bool)
	expected := make([]Winner, 0, total)
	for _, prize := range input.Prizes {
		for slot := uint32(1); slot <= prize.Count; slot++ {
			expected = append(expected, Winner{PrizeID: prize.ID, Slot: slot})
		}
	}
	for index, winner := range outcome.Winners {
		if !eligible[winner.ParticipantID] || seen[winner.ParticipantID] || winner.Slot == 0 || winner.Slot > prizes[winner.PrizeID] || winner.PrizeID != expected[index].PrizeID || winner.Slot != expected[index].Slot {
			return contracts.NewFault(contracts.InvalidState)
		}
		if slots[winner.PrizeID] == nil {
			slots[winner.PrizeID] = make(map[uint32]bool)
		}
		if slots[winner.PrizeID][winner.Slot] {
			return contracts.NewFault(contracts.InvalidState)
		}
		slots[winner.PrizeID][winner.Slot] = true
		seen[winner.ParticipantID] = true
	}
	return nil
}

type FinalizedError struct{ CollectionID contracts.CollectionID }

func (failure *FinalizedError) Error() string { return ErrAlreadyFinalized.Error() }
func (failure *FinalizedError) Unwrap() error { return ErrAlreadyFinalized }
