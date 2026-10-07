package roundlifecycle

import (
	"errors"
	"reflect"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestCollectionPageQueryIndependentBoundsAndAnchorOffset(t *testing.T) {
	for _, test := range []struct {
		name  string
		query CollectionPageQuery
		valid bool
	}{
		{"limit-zero", CollectionPageQuery{}, false}, {"limit-one", CollectionPageQuery{Limit: 1}, true},
		{"limit-49", CollectionPageQuery{Limit: 49}, true}, {"limit-50", CollectionPageQuery{Limit: 50}, true},
		{"limit-51", CollectionPageQuery{Limit: 51}, false}, {"limit-max", CollectionPageQuery{Limit: ^uint32(0)}, false},
		{"unanchored-offset-one", CollectionPageQuery{Limit: 50, Offset: 1}, true},
		{"unanchored-offset-max", CollectionPageQuery{Limit: 50, Offset: ^uint32(0)}, true},
		{"anchor-offset-zero", CollectionPageQuery{Limit: 50, RoundID: "anchor"}, true},
		{"anchor-offset-one", CollectionPageQuery{Limit: 50, RoundID: "anchor", Offset: 1}, false},
		{"anchor-offset-max", CollectionPageQuery{Limit: 50, RoundID: "anchor", Offset: ^uint32(0)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := test.query
			err := test.query.Validate()
			if (err == nil) != test.valid || !reflect.DeepEqual(before, test.query) {
				t.Fatal("page query validity or immutability changed")
			}
			if !test.valid {
				var fault contracts.Fault
				if !errors.As(err, &fault) || fault.Code != contracts.InvalidInput {
					t.Fatal("page query safe error contract", err)
				}
			}
		})
	}
}
