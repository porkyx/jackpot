package sqlite

import (
	"context"
	"database/sql"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func validateBadgeCandidateSet(collection rl.FrozenCollection, input rl.RoundInput, used map[contracts.ParticipantID]bool) error {
	if input.BadgeRules == nil {
		return nil
	}
	index := 0
	for _, participant := range collection.Participants {
		drawable, err := rl.ParticipantDrawable(participant, input.BadgeRules)
		if err != nil {
			return err
		}
		if drawable && !used[participant.ID] {
			if index >= len(input.CandidateIDs) || input.CandidateIDs[index] != participant.ID {
				return contracts.NewFault(contracts.InvalidInput)
			}
			index++
		}
	}
	if index != len(input.CandidateIDs) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return nil
}

func txValidateBadgeAdmission(ctx context.Context, tx *sql.Tx, request rl.AdmitRoundRequest) error {
	// Legacy admissions need only the immutable metadata to prove that an
	// omitted rule is still uniform; do not decode every comment a second time.
	if request.Input.BadgeRules == nil {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT frozen_json FROM collections WHERE id=?", request.CollectionID).Scan(&raw); err != nil {
			return err
		}
		var frozen rl.FrozenCollection
		if err := decodeStored(raw, &frozen); err != nil {
			return err
		}
		if frozen.Filters.BadgeRules != nil {
			return contracts.NewFault(contracts.InvalidInput)
		}
		return nil
	}
	collection, _, err := txCollection(ctx, tx, request.CollectionID)
	if err != nil {
		return err
	}
	if err := rl.ValidateBadgeInput(collection, request.Input); err != nil {
		return err
	}
	if request.Operation.Kind != "Rerun" {
		return nil // Retry/due must instead match their existing immutable input.
	}
	rows, err := tx.QueryContext(ctx, "SELECT participant_id FROM winners WHERE collection_id=?", request.CollectionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	used := make(map[contracts.ParticipantID]bool)
	for rows.Next() {
		var id contracts.ParticipantID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if len(used) >= 100000 || used[id] {
			return contracts.NewFault(contracts.InvalidState)
		}
		used[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return validateBadgeCandidateSet(collection, request.Input, used)
}
