package sqlite

import (
	"context"
	"database/sql"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

// ReadCollectionPage validates every past round in this committed snapshot while
// retaining only the requested page and latest record. Paging never hides damaged
// history, and consumed participants are counted across every validated outcome.
func (store *Store) ReadCollectionPage(ctx context.Context, id contracts.CollectionID, query rl.CollectionPageQuery) (rl.CollectionPage, error) {
	if err := readContext(ctx, string(id)); err != nil {
		return rl.CollectionPage{}, err
	}
	if err := query.Validate(); err != nil {
		return rl.CollectionPage{}, err
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rl.CollectionPage{}, err
	}
	defer tx.Rollback()
	frozen, revision, err := txCollection(ctx, tx, id)
	if err != nil {
		return rl.CollectionPage{}, err
	}
	ids, err := roundIDs(ctx, tx, "SELECT id FROM rounds WHERE collection_id=? ORDER BY number", id)
	if err != nil {
		return rl.CollectionPage{}, err
	}
	if len(ids) == 0 {
		return rl.CollectionPage{}, contracts.NewFault(contracts.InvalidState)
	}
	total := uint32(len(ids)) // roundIDs enforces its 100,000-row bound.
	offset := query.Offset
	anchorFound := query.RoundID == ""
	if query.RoundID != "" {
		for index, roundID := range ids {
			if roundID == query.RoundID {
				offset = (total - 1 - uint32(index)) / query.Limit * query.Limit
				anchorFound = true
				break
			}
		}
	}
	var start, end uint32
	if offset < total {
		end = total - offset
		if end > query.Limit {
			start = end - query.Limit
		}
	}
	page := rl.CollectionPage{Frozen: frozen, Revision: revision, Total: total, Offset: offset, Rounds: make([]rl.RoundRecord, 0, query.Limit)}
	exists := make(map[contracts.ParticipantID]bool, len(frozen.Participants))
	selected := uint32(0)
	for _, participant := range frozen.Participants {
		exists[participant.ID] = true
		if participant.Included {
			selected++
		}
	}
	consumed := make(map[contracts.ParticipantID]bool)
	invalidConsumption := false
	invalidHistory := false
	for index, roundID := range ids {
		round, err := txRound(ctx, tx, roundID)
		if err != nil {
			return rl.CollectionPage{}, err
		}
		if round.Number != uint32(index)+1 || round.CollectionID != id {
			invalidHistory = true
		}
		if uint32(index) >= start && uint32(index) < end {
			page.Rounds = append(page.Rounds, round)
		}
		if index == len(ids)-1 {
			page.LatestRound = round
		}
		if round.Outcome != nil {
			for _, winner := range round.Outcome.Winners {
				if !exists[winner.ParticipantID] || consumed[winner.ParticipantID] {
					invalidConsumption = true
					continue
				}
				consumed[winner.ParticipantID] = true
			}
		}
	}
	// Keep storage/validation failures ahead of the previous desktop aggregate
	// projection guards. Even an unknown anchor cannot skip all-past validation.
	if invalidHistory || invalidConsumption || len(consumed) > int(selected) {
		return rl.CollectionPage{}, contracts.NewFault(contracts.InvalidState)
	}
	if !anchorFound {
		return rl.CollectionPage{}, rl.ErrNotFound
	}
	page.ConsumedCount = uint32(len(consumed))
	if err = tx.Commit(); err != nil {
		return rl.CollectionPage{}, err
	}
	return page, nil
}
