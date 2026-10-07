package draw

import (
	"context"
	"reflect"

	"github.com/porkyx/jackpot/internal/contracts"
)

const CategoryAlgorithmVersion = "badge-group-weighted-v1"

// ValidateCategories keeps the legacy input unchanged when no badge rules were
// supplied. Explicit rules always carry one category for each frozen candidate.
func ValidateCategories(candidates []contracts.ParticipantID, categories []contracts.BadgeCategory, prizes []Prize, rules *contracts.BadgeRules) error {
	if err := Validate(candidates, prizes); err != nil {
		return err
	}
	if rules == nil {
		if len(categories) != 0 {
			return contracts.NewFault(contracts.InvalidInput)
		}
		return nil
	}
	if err := rules.Validate(); err != nil {
		return err
	}
	if len(categories) != len(candidates) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	positive := 0
	for _, category := range categories {
		rule, err := rules.Rule(category)
		if err != nil {
			return err
		}
		if rule.Weight > 0 {
			positive++
		}
	}
	total := uint32(0)
	for _, prize := range prizes {
		total += prize.Count
	}
	if rules.WeightingEnabled && int(total) > positive {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return nil
}

// SampleCategories selects a nonempty group by its relative weight, then a
// uniformly random participant within that group. Entropy.Intn implements the
// bounded unbiased draw; no modulo or population multiplier is used here.
func SampleCategories(ctx context.Context, candidates []contracts.ParticipantID, categories []contracts.BadgeCategory, prizes []Prize, rules *contracts.BadgeRules, entropy Entropy) ([]Winner, error) {
	if ctx == nil || entropy == nil || ((reflect.ValueOf(entropy).Kind() == reflect.Pointer || reflect.ValueOf(entropy).Kind() == reflect.Func) && reflect.ValueOf(entropy).IsNil()) {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateCategories(candidates, categories, prizes, rules); err != nil {
		return nil, err
	}
	if rules == nil || !rules.WeightingEnabled {
		return Sample(ctx, candidates, prizes, entropy)
	}
	order := contracts.BadgeCategories()
	var pools [6][]contracts.ParticipantID
	var weights [6]uint64
	for index, category := range order {
		rule, _ := rules.Rule(category) // category comes from the closed enum.
		weights[index] = uint64(rule.Weight)
		for candidate, actual := range categories {
			if actual == category && rule.Weight > 0 {
				pools[index] = append(pools[index], candidates[candidate])
			}
		}
	}
	result := make([]Winner, 0, 10)
	for _, prize := range prizes {
		for slot := uint32(1); slot <= prize.Count; slot++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			total := uint64(0)
			for index, pool := range pools {
				if len(pool) > 0 {
					total += weights[index]
				}
			}
			if total == 0 {
				return nil, contracts.NewFault(contracts.InvalidState)
			}
			choice, err := entropy.Intn(ctx, total)
			if err != nil {
				return nil, err
			}
			if choice >= total {
				return nil, contracts.NewFault(contracts.InvalidState)
			}
			selected := -1
			for index, pool := range pools {
				if len(pool) == 0 {
					continue
				}
				if choice < weights[index] {
					selected = index
					break
				}
				choice -= weights[index]
			}
			if selected < 0 {
				return nil, contracts.NewFault(contracts.InvalidState)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			pool := pools[selected]
			person, err := entropy.Intn(ctx, uint64(len(pool)))
			if err != nil {
				return nil, err
			}
			if person >= uint64(len(pool)) {
				return nil, contracts.NewFault(contracts.InvalidState)
			}
			result = append(result, Winner{ParticipantID: pool[person], PrizeID: prize.ID, Slot: slot})
			pool[person] = pool[len(pool)-1]
			pools[selected] = pool[:len(pool)-1]
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
