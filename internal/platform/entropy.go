package platform

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"math/big"

	"github.com/porkyx/jackpot/internal/draw"
)

type Entropy struct{ reader io.Reader }

var ErrEntropyExhausted = errors.New("entropy source failed to produce a sample")
var _ draw.Entropy = (*Entropy)(nil)

func NewEntropy(reader io.Reader) (*Entropy, error) {
	if reader == nil {
		return nil, errors.New("nil entropy reader")
	}
	return &Entropy{reader: reader}, nil
}
func SystemEntropy() *Entropy { return &Entropy{reader: rand.Reader} }
func (entropy *Entropy) Intn(ctx context.Context, bound uint64) (uint64, error) {
	if ctx == nil || bound == 0 {
		return 0, errors.New("invalid entropy request")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	value, err := rand.Int(&boundedEntropyReader{ctx: ctx, reader: entropy.reader, remaining: 256}, new(big.Int).SetUint64(bound))
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return value.Uint64(), nil
}

// Retain crypto/rand's unbiased sampling while bounding a faulty reader's
// rejection or zero-progress loop. No worker can outlive the call.
type boundedEntropyReader struct {
	ctx       context.Context
	reader    io.Reader
	remaining int
}

func (reader *boundedEntropyReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if reader.remaining == 0 {
		return 0, ErrEntropyExhausted
	}
	reader.remaining--
	n, err := reader.reader.Read(buffer)
	if n < 0 || n > len(buffer) {
		return 0, ErrEntropyExhausted
	}
	return n, err
}
