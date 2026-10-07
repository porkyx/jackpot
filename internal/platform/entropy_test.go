package platform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"testing"
)

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(buffer []byte) (int, error) { return f(buffer) }

func TestEntropyRejectsNilReader(t *testing.T) {
	entropy, err := NewEntropy(nil)
	if entropy != nil || err == nil {
		t.Fatalf("%v/%v", entropy, err)
	}
}
func TestEntropyInvalidRequestsDoNotRead(t *testing.T) {
	calls := 0
	entropy, _ := NewEntropy(readerFunc(func([]byte) (int, error) { calls++; return 0, io.EOF }))
	for _, tc := range []struct {
		ctx   context.Context
		bound uint64
	}{{nil, 10}, {context.Background(), 0}} {
		value, err := entropy.Intn(tc.ctx, tc.bound)
		if value != 0 || err == nil {
			t.Fatalf("%d/%v", value, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input consumed entropy")
	}
}
func TestEntropyBoundsAndRejectionHaveNoModuloBias(t *testing.T) {
	for _, bound := range []uint64{1, 2, 255, 256, 257, math.MaxUint64} {
		entropy, _ := NewEntropy(bytes.NewReader(make([]byte, 8)))
		value, err := entropy.Intn(context.Background(), bound)
		if err != nil || value != 0 {
			t.Fatalf("bound %d: %d/%v", bound, value, err)
		}
	}
	// 255 must be rejected for bound 255; a modulo implementation returns zero.
	entropy, _ := NewEntropy(bytes.NewReader([]byte{255, 254}))
	value, err := entropy.Intn(context.Background(), 255)
	if err != nil || value != 254 {
		t.Fatalf("biased sample: %d/%v", value, err)
	}
}
func TestEntropyFirstNthAndContinuousFailuresNeverReturnPartialSample(t *testing.T) {
	sentinel := errors.New("source failed")
	for _, failedAt := range []int{1, 3, 0} {
		t.Run(string(rune('0'+failedAt)), func(t *testing.T) {
			calls := 0
			entropy, _ := NewEntropy(readerFunc(func(buffer []byte) (int, error) {
				calls++
				if failedAt == 0 || calls == failedAt {
					return 0, sentinel
				}
				buffer[0] = 7
				return 1, nil
			}))
			for attempt := 1; attempt <= 4; attempt++ {
				value, err := entropy.Intn(context.Background(), 256)
				if failedAt == 0 || attempt == failedAt {
					if value != 0 || !errors.Is(err, sentinel) {
						t.Fatalf("attempt %d: %d/%v", attempt, value, err)
					}
				} else if value != 7 || err != nil {
					t.Fatalf("attempt %d: %d/%v", attempt, value, err)
				}
			}
			if calls != 4 {
				t.Fatalf("unexpected reads: %d", calls)
			}
		})
	}
}
func TestEntropyCancellationBeforeAndAfterReadNeverPublishesSample(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before", false: "after"}[before], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			entropy, _ := NewEntropy(readerFunc(func(buffer []byte) (int, error) { calls++; buffer[0] = 3; cancel(); return 1, nil }))
			if before {
				cancel()
			}
			value, err := entropy.Intn(ctx, 256)
			if value != 0 || !errors.Is(err, context.Canceled) {
				t.Fatalf("%d/%v", value, err)
			}
			if before && calls != 0 || !before && calls != 1 {
				t.Fatalf("reads=%d", calls)
			}
		})
	}
}
func TestEntropyCancellationDuringRejectionStopsFurtherReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	entropy, _ := NewEntropy(readerFunc(func(buffer []byte) (int, error) { calls++; buffer[0] = 255; cancel(); return 1, nil }))
	value, err := entropy.Intn(ctx, 255)
	if value != 0 || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("%d/%v/%d", value, err, calls)
	}
}
func TestEntropyFaultyReadersHaveBoundedCalls(t *testing.T) {
	for _, mode := range []string{"rejection", "zero progress", "negative count", "oversize count"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			entropy, _ := NewEntropy(readerFunc(func(buffer []byte) (int, error) {
				calls++
				switch mode {
				case "rejection":
					buffer[0] = 255
					return 1, nil
				case "negative count":
					return -1, nil
				case "oversize count":
					return len(buffer) + 1, nil
				default:
					return 0, nil
				}
			}))
			value, err := entropy.Intn(context.Background(), 255)
			if value != 0 || !errors.Is(err, ErrEntropyExhausted) || calls > 256 {
				t.Fatalf("unbounded fault: %d/%v/%d", value, err, calls)
			}
			if (mode == "rejection" || mode == "zero progress") && calls != 256 {
				t.Fatalf("budget not enforced: %d", calls)
			}
		})
	}
}
func TestSystemEntropyHasValidSingleCandidateWithoutReaderConsumption(t *testing.T) {
	entropy := SystemEntropy()
	value, err := entropy.Intn(context.Background(), 1)
	if entropy.reader == nil || err != nil || value != 0 {
		t.Fatalf("%d/%v", value, err)
	}
}
