package roundlifecycle

import (
	"context"
	"crypto/sha256"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/porkyx/jackpot/internal/contracts"
)

func TestPublicFingerprintExactHundredMiBPreimageBoundaryAndOneByteOver(t *testing.T) {
	const prefix = `{"Kind":"Boundary","Payload":"`
	const suffix = `"}`
	const limit = 100 << 20
	payload := strings.Repeat("x", limit-len(prefix)-len(suffix))
	expected := sha256.New()
	if _, err := io.WriteString(expected, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(expected, strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(expected, suffix); err != nil {
		t.Fatal(err)
	}
	actual, err := publicFingerprint("Boundary", payload)
	if err != nil || !reflect.DeepEqual(actual[:], expected.Sum(nil)) {
		t.Fatal("exact preimage bound rejected or canonical digest changed", err)
	}
	rejected, err := publicFingerprint("Boundary", payload+"x")
	requireBoundaryFault(t, err, contracts.InvalidInput)
	if rejected != ([32]byte{}) {
		t.Fatal("oversized payload exposed fingerprint")
	}
}

func TestPublicFingerprintMarshalFailureHasZeroDigestAndSafeTypedFault(t *testing.T) {
	digest, err := publicFingerprint("Boundary", func() {})
	requireBoundaryFault(t, err, contracts.InvalidInput)
	if digest != ([32]byte{}) {
		t.Fatal("unrepresentable payload exposed digest")
	}
}

func TestPrepareCreateUnrepresentableTimestampRefusesBeforeStorage(t *testing.T) {
	storage := boundaryPreparingStorage()

	service := boundaryService(t, storage)
	request := boundaryCreateRequest()
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	request.Collection.Article.PostedAt = &invalid
	prepared, err := service.PrepareCreate(context.Background(), request)
	requireBoundaryFault(t, err, contracts.InvalidInput)
	if prepared != nil || storage.operations.Load()+storage.rounds.Load()+storage.admissions.Load() != 0 {
		t.Fatal("unrepresentable timestamp had pre-admission effects")
	}
	request.Collection.Article.PostedAt = nil
	next, err := service.PrepareCreate(context.Background(), request)
	if err != nil {
		t.Fatal("following valid preparation failed", err)
	}
	next.Close()
	if storage.operations.Load() != 1 {
		t.Fatal("failure blocked safe recovery")
	}
}
