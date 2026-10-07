package contracts

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestLocalCreateAndCollectionWireHaveNoPasswordOrLockFields(t *testing.T) {
	for _, value := range []any{CreateCollectionRequest{}, CollectionData{}} {
		typ := reflect.TypeOf(value)
		for _, field := range []string{"Password", "Unlocked", "Credential"} {
			if _, exists := typ.FieldByName(field); exists {
				t.Fatalf("%s still exposes %s", typ.Name(), field)
			}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{`"password"`, `"unlocked"`, `"credential"`} {
			if strings.Contains(string(raw), field) {
				t.Fatalf("%s wire still contains %s", typ.Name(), field)
			}
		}
	}
}
