package main

import (
	"context"
	"errors"
	"github.com/porkyx/jackpot/internal/contracts"
	"net/http"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
)

// Delivery faults only; the collector uses the production request and context.
type fixtureNetwork struct {
	mu                 sync.Mutex
	armed, active      string
	entered, cancelled uint32
	blocked            bool
	barrier            chan struct{}
}
type FixtureNetworkReport struct {
	Armed          string `json:"armed"`
	Active         string `json:"active"`
	Page2Entered   uint32 `json:"page2Entered"`
	Page2Cancelled uint32 `json:"page2Cancelled"`
	Blocked        bool   `json:"blocked"`
}

func (network *fixtureNetwork) arm(mode string) error {
	if mode != "normal" && mode != "fail-page2" && mode != "block-page2" {
		return errors.New("unsupported network fixture mode")
	}
	network.mu.Lock()
	defer network.mu.Unlock()
	if network.blocked {
		return errors.New("network fixture request still blocked")
	}
	network.armed = mode
	network.barrier = make(chan struct{})
	return nil
}
func (network *fixtureNetwork) begin() {
	network.mu.Lock()
	defer network.mu.Unlock()
	network.active = network.armed
	network.armed = ""
}
func (network *fixtureNetwork) snapshot() FixtureNetworkReport {
	network.mu.Lock()
	defer network.mu.Unlock()
	return FixtureNetworkReport{Armed: network.armed, Active: network.active, Page2Entered: network.entered, Page2Cancelled: network.cancelled, Blocked: network.blocked}
}
func (network *fixtureNetwork) page2(ctx context.Context) (bool, error) {
	network.mu.Lock()
	mode := network.active
	if mode != "fail-page2" && mode != "block-page2" {
		network.mu.Unlock()
		return false, nil
	}
	network.entered++
	if network.barrier != nil {
		close(network.barrier)
		network.barrier = nil
	}
	if mode == "fail-page2" {
		network.active = ""
		network.mu.Unlock()
		return true, nil
	}
	network.blocked = true
	network.mu.Unlock()
	<-ctx.Done()
	network.mu.Lock()
	network.cancelled++
	network.blocked = false
	network.active = ""
	network.mu.Unlock()
	return true, ctx.Err()
}
func (probe *Probe) ControlFixture(mode string) (FixtureNetworkReport, error) {
	if probe.transport == nil || !probe.transport.large {
		return FixtureNetworkReport{}, errors.New("network controls require the large fixture")
	}
	if err := probe.transport.network.arm(mode); err != nil {
		return FixtureNetworkReport{}, err
	}
	return probe.transport.network.snapshot(), nil
}
func (probe *Probe) DraftState(ctx context.Context) (contracts.DraftData, error) {
	summary := probe.draft.Summary()
	if summary == nil {
		return contracts.DraftData{}, errors.New("no active draft")
	}
	return probe.draft.Get(ctx, contracts.DraftQuery{BackendSessionID: probe.session, DraftID: summary.DraftID})
}
func (transport *fixtureTransport) failureResponse(request *http.Request) *http.Response {
	raw := "fixture page2 rejected"
	transport.calls.Add(1)
	return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"text/plain"}}, Body: &fixtureBody{Reader: strings.NewReader(raw), closed: &transport.closed}, Request: request, ContentLength: int64(len(raw))}
}

type MemoryReport struct {
	MemoryLimitBytes string `json:"memoryLimitBytes"`
	HeapAlloc        uint64 `json:"heapAlloc"`
	HeapInuse        uint64 `json:"heapInuse"`
	HeapIdle         uint64 `json:"heapIdle"`
	HeapReleased     uint64 `json:"heapReleased"`
	StackInuse       uint64 `json:"stackInuse"`
	NumGC            uint32 `json:"numGC"`
	Goroutines       int    `json:"goroutines"`
	DBOpen           int    `json:"dbOpen"`
	DBInUse          int    `json:"dbInUse"`
}

func (probe *Probe) Memory() MemoryReport {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	samples := []metrics.Sample{{Name: "/gc/gomemlimit:bytes"}}
	metrics.Read(samples)
	report := MemoryReport{MemoryLimitBytes: strconv.FormatUint(samples[0].Value.Uint64(), 10), HeapAlloc: stats.HeapAlloc, HeapInuse: stats.HeapInuse, HeapIdle: stats.HeapIdle, HeapReleased: stats.HeapReleased, StackInuse: stats.StackInuse, NumGC: stats.NumGC, Goroutines: runtime.NumGoroutine()}
	if probe.diagnostics != nil {
		db := probe.diagnostics.Stats()
		report.DBOpen = db.OpenConnections
		report.DBInUse = db.InUse
	}
	return report
}

type ExportFixtureReport struct {
	Saves      uint32 `json:"saves"`
	Copies     uint32 `json:"copies"`
	SavePath   string `json:"savePath"`
	CopiedText string `json:"copiedText"`
}

func (probe *Probe) ExportState() ExportFixtureReport {
	probe.exports.mu.Lock()
	defer probe.exports.mu.Unlock()
	return ExportFixtureReport{Saves: probe.exports.saves, Copies: probe.exports.copies, SavePath: probe.exports.savePath, CopiedText: probe.exports.copiedText}
}
