package roundlifecycle

import "github.com/porkyx/jackpot/internal/contracts"

// Offset counts backwards from the newest round. An anchor resolves the page
// containing that round; anchor queries must start with Offset zero.
type CollectionPageQuery struct {
	Offset  uint32
	Limit   uint32
	RoundID contracts.RoundID
}

func (query CollectionPageQuery) Validate() error {
	if query.Limit == 0 || query.Limit > 50 || (query.RoundID != "" && query.Offset != 0) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return nil
}

// Every record belongs to one committed snapshot. Rounds contains at most 50
// records in increasing number order; LatestRound is present even on older pages.
// Frozen and all round graphs are caller-owned and must be treated as readonly.
type CollectionPage struct {
	Frozen        FrozenCollection
	Rounds        []RoundRecord
	LatestRound   RoundRecord
	Revision      contracts.Revision
	Total         uint32
	Offset        uint32
	ConsumedCount uint32
}
