package roundlifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestScheduleTimeQuickChoicesMutualExclusionAndKSTCalendarBoundaries(t *testing.T) {
	accepted := time.Date(2026, 10, 6, 14, 59, 45, 123, time.UTC)
	kst := time.FixedZone("Asia/Seoul", 9*3600)
	local := accepted.In(kst)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, kst)
	for _, delay := range []uint32{10, 30, 60, 120} {
		request := SetScheduleRequest{QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"}
		got, err := scheduleTime(request, accepted)
		if err != nil || !got.Equal(accepted.Add(time.Duration(delay)*time.Second)) {
			t.Fatal(got, err)
		}
	}
	for _, delay := range []uint32{0, 1, 9, 11, 29, 31, 59, 61, 119, 121, ^uint32(0)} {
		if _, err := scheduleTime(SetScheduleRequest{QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"}, accepted); err == nil {
			t.Fatal(delay)
		}
	}
	delay := uint32(10)
	for _, request := range []SetScheduleRequest{{Timezone: "Asia/Seoul"}, {ScheduledAt: accepted.Add(time.Minute), QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"}, {QuickDelaySeconds: &delay}, {QuickDelaySeconds: &delay, Timezone: "UTC"}, {ScheduledAt: time.Time{}, Timezone: "Asia/Seoul"}, {ScheduledAt: accepted.Add(time.Minute), Timezone: "Asia/Seoul"}} {
		if _, err := scheduleTime(request, accepted); err == nil {
			t.Fatal("invalid schedule accepted")
		}
	}
	if _, err := scheduleTime(SetScheduleRequest{QuickDelaySeconds: &delay, Timezone: "Asia/Seoul"}, time.Time{}); err == nil {
		t.Fatal("zero accepted clock")
	}
	for _, scheduled := range []time.Time{dayStart.AddDate(0, 0, 1), dayStart.AddDate(0, 0, 3).Add(23*time.Hour + 59*time.Minute)} {
		if got, err := scheduleTime(SetScheduleRequest{ScheduledAt: scheduled, Timezone: "Asia/Seoul"}, accepted); err != nil || !got.Equal(scheduled) {
			t.Fatal(got, err)
		}
	}
	for _, scheduled := range []time.Time{dayStart.Add(-time.Minute), dayStart.AddDate(0, 0, 4), accepted.Truncate(time.Minute), dayStart.AddDate(0, 0, 1).Add(time.Nanosecond), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := scheduleTime(SetScheduleRequest{ScheduledAt: scheduled, Timezone: "Asia/Seoul"}, accepted); err == nil {
			t.Fatal("direct date bound accepted", scheduled)
		}
	}
	exact := time.Date(2026, 10, 6, 0, 0, 50, 0, time.UTC)
	if _, err := scheduleTime(SetScheduleRequest{ScheduledAt: exact.Add(10 * time.Second), Timezone: "Asia/Seoul"}, exact); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduleTime(SetScheduleRequest{ScheduledAt: exact.Add(10 * time.Second), Timezone: "Asia/Seoul"}, exact.Add(time.Nanosecond)); err == nil {
		t.Fatal("direct min boundary")
	}
}
func TestOutcomeRequiresOrderedSlotsExactCountsAndEligibleDistinctWinners(t *testing.T) {
	input := RoundInput{CandidateIDs: []contracts.ParticipantID{"a", "b", "c"}, Prizes: []Prize{{ID: "one", Count: 2}, {ID: "two", Count: 1}}, Mode: ImmediateMode}
	good := Outcome{Winners: []Winner{{ParticipantID: "a", PrizeID: "one", Slot: 1}, {ParticipantID: "b", PrizeID: "one", Slot: 2}, {ParticipantID: "c", PrizeID: "two", Slot: 1}}, ExecutedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), AlgorithmVersion: "test", AppVersion: "test"}
	if err := ValidateOutcome(input, good); err != nil {
		t.Fatal(err)
	}
	cases := []func(*Outcome){func(o *Outcome) { o.ExecutedAt = time.Time{} }, func(o *Outcome) { o.ExecutedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, func(o *Outcome) { o.AlgorithmVersion = "" }, func(o *Outcome) { o.AppVersion = "" }, func(o *Outcome) { o.Winners = nil }, func(o *Outcome) { o.Winners = o.Winners[:2] }, func(o *Outcome) { o.Winners[0].ParticipantID = "absent" }, func(o *Outcome) { o.Winners[1].ParticipantID = "a" }, func(o *Outcome) { o.Winners[0].PrizeID = "absent" }, func(o *Outcome) { o.Winners[0].Slot = 0 }, func(o *Outcome) { o.Winners[0].Slot = 3 }, func(o *Outcome) { o.Winners[1].Slot = 1 }, func(o *Outcome) { o.Winners[0], o.Winners[1] = o.Winners[1], o.Winners[0] }, func(o *Outcome) { o.Winners[2].PrizeID = "one" }}
	for index, mutate := range cases {
		copy := good
		copy.Winners = append([]Winner(nil), good.Winners...)
		mutate(&copy)
		if err := ValidateOutcome(input, copy); err == nil {
			t.Fatal("outcome mutation accepted", index)
		}
	}
	badInput := input
	badInput.Mode = "bad"
	if err := ValidateOutcome(badInput, good); err == nil {
		t.Fatal("invalid input")
	}
}
func TestInputModeMessageBoundsAndSafeErrorProjection(t *testing.T) {
	good := RoundInput{CandidateIDs: []contracts.ParticipantID{"a"}, Prizes: []Prize{{ID: "p", Count: 1}}, Mode: ImmediateMode}
	for _, mode := range []string{ImmediateMode, ReservationMode} {
		input := good
		input.Mode = mode
		input.Message = strings.Repeat("😀", 10)
		if err := ValidateRoundInput(input); err != nil {
			t.Fatal(err)
		}
	}
	for _, message := range []string{strings.Repeat("a", 21), string([]byte{255})} {
		input := good
		input.Message = message
		if err := ValidateRoundInput(input); err == nil {
			t.Fatal("invalid message")
		}
	}
	if contextError(nil) == nil || contextError(context.Background()) != nil {
		t.Fatal("context guard")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(contextError(ctx), context.Canceled) {
		t.Fatal("cancel")
	}
	for _, err := range []error{nil, ErrOperationConflict, ErrAlreadyFinalized, context.Canceled, context.DeadlineExceeded, contracts.NewFault(contracts.InvalidInput)} {
		if storageError(err) != err {
			t.Fatal("safe error changed")
		}
	}
	var fault contracts.Fault
	if !errors.As(storageError(errors.New("private database detail")), &fault) || fault.Code != contracts.StorageUnavailable {
		t.Fatal("unsafe error exposed")
	}
	failure := &FinalizedError{CollectionID: "c"}
	if !errors.Is(failure, ErrAlreadyFinalized) || failure.Error() != "AlreadyFinalized" {
		t.Fatal(failure)
	}
}
