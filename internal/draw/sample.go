package draw

import (
	"context"
	"crypto/rand"
	"io"
	"math/big"
	"reflect"
	"unicode/utf8"

	"github.com/porkyx/jackpot/internal/contracts"
)

const AlgorithmVersion = "fisher-yates-partial-v1"

type Prize struct {
	ID, Name string
	Count    uint32
}
type Winner struct {
	ParticipantID contracts.ParticipantID
	PrizeID       string
	Slot          uint32
}
type CryptoEntropy struct{ reader io.Reader }

func (entropy CryptoEntropy) Intn(ctx context.Context, bound uint64) (uint64, error) {
	if ctx == nil || bound == 0 {
		return 0, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	reader := entropy.reader
	if reader == nil {
		reader = rand.Reader
	}
	value, err := rand.Int(reader, new(big.Int).SetUint64(bound))
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return value.Uint64(), nil
}
func UTF16Length(value string) (int, bool) {
	if !utf8.ValidString(value) {
		return 0, false
	}
	length := 0
	for _, character := range value {
		length++
		if character > 0xffff {
			length++
		}
	}
	return length, true
}
func Validate(candidates []contracts.ParticipantID, prizes []Prize) error {
	if len(candidates) < 1 || len(candidates) > 100000 || len(prizes) < 1 || len(prizes) > 10 {
		return contracts.NewFault(contracts.InvalidInput)
	}
	participants := make(map[contracts.ParticipantID]bool, len(candidates))
	for _, id := range candidates {
		if id == "" || participants[id] {
			return contracts.NewFault(contracts.InvalidInput)
		}
		participants[id] = true
	}
	identities := make(map[string]bool, len(prizes))
	total := uint32(0)
	for _, prize := range prizes {
		length, valid := UTF16Length(prize.Name)
		if prize.ID == "" || identities[prize.ID] || !valid || length > 20 || prize.Count < 1 || prize.Count > 10 {
			return contracts.NewFault(contracts.InvalidInput)
		}
		identities[prize.ID] = true
		total += prize.Count
	}
	if total > 10 || int(total) > len(candidates) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return nil
}
func Sample(ctx context.Context, candidates []contracts.ParticipantID, prizes []Prize, entropy Entropy) ([]Winner, error) {
	if ctx == nil || entropy == nil || ((reflect.ValueOf(entropy).Kind() == reflect.Pointer || reflect.ValueOf(entropy).Kind() == reflect.Func) && reflect.ValueOf(entropy).IsNil()) {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := Validate(candidates, prizes); err != nil {
		return nil, err
	}
	pool := append([]contracts.ParticipantID(nil), candidates...)
	result := make([]Winner, 0, 10)
	for _, prize := range prizes {
		for slot := uint32(1); slot <= prize.Count; slot++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			offset := len(result)
			choice, err := entropy.Intn(ctx, uint64(len(pool)-offset))
			if err != nil {
				return nil, err
			}
			if choice >= uint64(len(pool)-offset) {
				return nil, contracts.NewFault(contracts.InvalidState)
			}
			selected := offset + int(choice)
			pool[offset], pool[selected] = pool[selected], pool[offset]
			result = append(result, Winner{ParticipantID: pool[offset], PrizeID: prize.ID, Slot: slot})
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
