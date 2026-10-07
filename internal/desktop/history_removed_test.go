package desktop

import (
	"reflect"
	"testing"
)

func TestRoundServicePublicSurfaceRemovesHistoryAndKeepsLocalResultCommands(t *testing.T) {
	service := reflect.TypeOf((*RoundService)(nil))
	if _, found := service.MethodByName("ListCollections"); found {
		t.Fatal("history listing remains exposed by the Wails service")
	}
	for _, name := range []string{"GetCollection", "CreateCollection", "Rerun", "SetSchedule", "CancelSchedule", "RetryRound", "QueryFrozenParticipants", "QueryFrozenComments"} {
		if _, found := service.MethodByName(name); !found {
			t.Fatal("removing history removed a required local result command", name)
		}
	}
}
