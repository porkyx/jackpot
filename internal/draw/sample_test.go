package draw

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

type entropyFunc func(context.Context, uint64) (uint64, error)

func (function entropyFunc) Intn(ctx context.Context, bound uint64) (uint64, error) {
	return function(ctx, bound)
}

type readerFunc func([]byte) (int, error)

func (function readerFunc) Read(raw []byte) (int, error) { return function(raw) }
func TestUTF16LengthCountsCodeUnitsWithoutTrimming(t *testing.T) {
	for _, entry := range []struct {
		raw    string
		length int
		valid  bool
	}{{"", 0, true}, {"  ", 2, true}, {"가a", 2, true}, {"😀", 2, true}, {strings.Repeat("😀", 10), 20, true}, {string([]byte{255}), 0, false}} {
		length, valid := UTF16Length(entry.raw)
		if length != entry.length || valid != entry.valid {
			t.Fatalf("%q length %d/%t", entry.raw, length, valid)
		}
	}
}
func TestInputValidationIndependentBoundsAndDuplicateIdentities(t *testing.T) {
	candidates := []contracts.ParticipantID{"one", "two"}
	prizes := []Prize{{ID: "p", Count: 1}}
	cases := []struct {
		name   string
		ids    []contracts.ParticipantID
		prizes []Prize
	}{
		{"nil candidates", nil, prizes}, {"empty candidates", []contracts.ParticipantID{}, prizes}, {"empty ID", []contracts.ParticipantID{""}, prizes}, {"duplicate IDs", []contracts.ParticipantID{"one", "one"}, prizes}, {"nil prizes", candidates, nil}, {"empty prizes", candidates, []Prize{}}, {"empty prize ID", candidates, []Prize{{Count: 1}}}, {"duplicate prize ID", candidates, []Prize{{ID: "p", Count: 1}, {ID: "p", Count: 1}}}, {"invalid UTF8", candidates, []Prize{{ID: "p", Count: 1, Name: string([]byte{255})}}}, {"name 21", candidates, []Prize{{ID: "p", Count: 1, Name: strings.Repeat("가", 21)}}}, {"count zero", candidates, []Prize{{ID: "p"}}}, {"count eleven", candidates, []Prize{{ID: "p", Count: 11}}}, {"count max", candidates, []Prize{{ID: "p", Count: math.MaxUint32}}}, {"more than candidates", candidates, []Prize{{ID: "p", Count: 3}}},
	}
	large := make([]contracts.ParticipantID, 100001)
	for index := range large {
		large[index] = contracts.ParticipantID(fmt.Sprint(index))
	}
	cases = append(cases, struct {
		name   string
		ids    []contracts.ParticipantID
		prizes []Prize
	}{"100001 candidates", large, prizes})
	ten := make([]Prize, 11)
	for index := range ten {
		ten[index] = Prize{ID: fmt.Sprint(index), Count: 1}
	}
	cases = append(cases, struct {
		name   string
		ids    []contracts.ParticipantID
		prizes []Prize
	}{"11 prizes", large[:100000], ten}, struct {
		name   string
		ids    []contracts.ParticipantID
		prizes []Prize
	}{"sum eleven", large[:100000], []Prize{{ID: "one", Count: 6}, {ID: "two", Count: 5}}})
	for _, entry := range cases {
		t.Run(entry.name, func(t *testing.T) {
			if err := Validate(entry.ids, entry.prizes); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	for _, entry := range []struct {
		ids    []contracts.ParticipantID
		prizes []Prize
	}{{candidates[:1], prizes}, {large[:100000], []Prize{{ID: "p", Count: 10, Name: strings.Repeat("😀", 10)}}}, {large[:100000], ten[:10]}} {
		if err := Validate(entry.ids, entry.prizes); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSampleEnumeratesEveryOrderedPairUniformlyAndPreservesInputs(t *testing.T) {
	ids := []contracts.ParticipantID{"a", "b", "c"}
	prizes := []Prize{{ID: "first", Name: "A", Count: 1}, {ID: "second", Name: "B", Count: 1}}
	beforeIDs := append([]contracts.ParticipantID(nil), ids...)
	beforePrizes := append([]Prize(nil), prizes...)
	pairs := make(map[string]int)
	for first := uint64(0); first < 3; first++ {
		for second := uint64(0); second < 2; second++ {
			calls := 0
			winners, err := Sample(context.Background(), ids, prizes, entropyFunc(func(ctx context.Context, bound uint64) (uint64, error) {
				calls++
				if calls == 1 {
					if bound != 3 {
						t.Fatal(bound)
					}
					return first, nil
				}
				if bound != 2 {
					t.Fatal(bound)
				}
				return second, nil
			}))
			if err != nil || calls != 2 || winners[0].ParticipantID == winners[1].ParticipantID || winners[0].PrizeID != "first" || winners[1].PrizeID != "second" || winners[0].Slot != 1 || winners[1].Slot != 1 {
				t.Fatal(winners, err)
			}
			pairs[string(winners[0].ParticipantID)+string(winners[1].ParticipantID)]++
		}
	}
	if len(pairs) != 6 {
		t.Fatal(pairs)
	}
	for pair, count := range pairs {
		if count != 1 {
			t.Fatal(pair, count)
		}
	}
	if !reflect.DeepEqual(ids, beforeIDs) || !reflect.DeepEqual(prizes, beforePrizes) {
		t.Fatal("sample mutated frozen input")
	}
	winners, err := Sample(context.Background(), ids, []Prize{{ID: "all", Count: 3}}, entropyFunc(func(_ context.Context, bound uint64) (uint64, error) { return bound - 1, nil }))
	if err != nil || winners[2].Slot != 3 {
		t.Fatal(winners, err)
	}
}
func TestSampleRejectsFailuresAndNeverReturnsPartialWinners(t *testing.T) {
	ids := []contracts.ParticipantID{"a", "b", "c"}
	prizes := []Prize{{ID: "p", Count: 2}}
	good := entropyFunc(func(context.Context, uint64) (uint64, error) { return 0, nil })
	if winners, err := Sample(nil, ids, prizes, good); err == nil || winners != nil {
		t.Fatal(winners, err)
	}
	if winners, err := Sample(context.Background(), ids, prizes, entropyFunc(nil)); err == nil || winners != nil {
		t.Fatal(winners, err)
	}
	if winners, err := Sample(context.Background(), ids, prizes, nil); err == nil || winners != nil {
		t.Fatal(winners, err)
	}
	if winners, err := Sample(context.Background(), nil, prizes, good); err == nil || winners != nil {
		t.Fatal(winners, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if winners, err := Sample(cancelled, ids, prizes, good); !errors.Is(err, context.Canceled) || winners != nil {
		t.Fatal(winners, err)
	}
	for _, nth := range []int{1, 2} {
		t.Run(fmt.Sprintf("entropy failure %d", nth), func(t *testing.T) {
			calls := 0
			winners, err := Sample(context.Background(), ids, prizes, entropyFunc(func(context.Context, uint64) (uint64, error) {
				calls++
				if calls == nth {
					return 0, io.ErrUnexpectedEOF
				}
				return 0, nil
			}))
			if !errors.Is(err, io.ErrUnexpectedEOF) || winners != nil || calls != nth {
				t.Fatal(winners, err, calls)
			}
		})
	}
	for _, count := range []uint32{1, 2} {
		ctx, cancel := context.WithCancel(context.Background())
		winners, err := Sample(ctx, ids, []Prize{{ID: "p", Count: count}}, entropyFunc(func(context.Context, uint64) (uint64, error) { cancel(); return 0, nil }))
		if !errors.Is(err, context.Canceled) || winners != nil {
			t.Fatal(winners, err)
		}
	}
	for _, choice := range []uint64{3, math.MaxUint64} {
		winners, err := Sample(context.Background(), ids, prizes, entropyFunc(func(context.Context, uint64) (uint64, error) { return choice, nil }))
		if err == nil || winners != nil {
			t.Fatal(winners, err)
		}
	}
	if !reflect.DeepEqual(ids, []contracts.ParticipantID{"a", "b", "c"}) {
		t.Fatal("failure mutated input")
	}
}
func TestCryptoEntropyBoundsRejectionFailuresAndCancellation(t *testing.T) {
	entropy := CryptoEntropy{}
	if _, err := entropy.Intn(nil, 1); err == nil {
		t.Fatal("nil context")
	}
	if _, err := entropy.Intn(context.Background(), 0); err == nil {
		t.Fatal("zero bound")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := entropy.Intn(ctx, 2); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, bound := range []uint64{1, 2, 257, math.MaxUint64} {
		entropy := CryptoEntropy{reader: bytes.NewReader(make([]byte, 8))}
		value, err := entropy.Intn(context.Background(), bound)
		if err != nil || value >= bound {
			t.Fatal(value, err)
		}
	}
	// 511 is rejected at bound 257; the next independently read value is 255.
	entropy = CryptoEntropy{reader: bytes.NewReader([]byte{1, 255, 0, 255})}
	value, err := entropy.Intn(context.Background(), 257)
	if err != nil || value != 255 {
		t.Fatal(value, err)
	}
	entropy = CryptoEntropy{reader: readerFunc(func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF })}
	if _, err = entropy.Intn(context.Background(), 2); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	entropy = CryptoEntropy{reader: readerFunc(func(raw []byte) (int, error) { clear(raw); cancel(); return len(raw), nil })}
	if _, err = entropy.Intn(ctx, 2); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	value, err = (CryptoEntropy{}).Intn(context.Background(), 1)
	if err != nil || value != 0 {
		t.Fatal(value, err)
	}
}
func FuzzSampleInputAndEntropyNeverPanicsOrLeaksPartialResults(f *testing.F) {
	f.Add([]byte{1, 2, 3}, uint8(2), uint64(0))
	f.Add([]byte{}, uint8(0), uint64(math.MaxUint64))
	f.Fuzz(func(t *testing.T, raw []byte, count uint8, choice uint64) {
		if len(raw) > 128 {
			raw = raw[:128]
		}
		ids := make([]contracts.ParticipantID, len(raw))
		for index, value := range raw {
			ids[index] = contracts.ParticipantID(string([]byte{value}))
		}
		before := append([]contracts.ParticipantID(nil), ids...)
		result, err := Sample(context.Background(), ids, []Prize{{ID: "p", Count: uint32(count)}}, entropyFunc(func(context.Context, uint64) (uint64, error) { return choice, nil }))
		if err != nil && result != nil {
			t.Fatal("partial result leaked")
		}
		if len(ids) > 0 && !reflect.DeepEqual(ids, before) {
			t.Fatal("input mutation")
		}
		if err == nil {
			if len(result) != int(count) {
				t.Fatal("count mismatch")
			}
			seen := map[contracts.ParticipantID]bool{}
			for _, winner := range result {
				if seen[winner.ParticipantID] {
					t.Fatal("duplicate winner")
				}
				seen[winner.ParticipantID] = true
			}
		}
	})
}
