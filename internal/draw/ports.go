package draw

import "context"

// Entropy supplies a uniform integer in [0,bound). Algorithms never own a seed.
type Entropy interface {
	Intn(context.Context, uint64) (uint64, error)
}
