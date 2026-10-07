package desktop

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"testing"

	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/platform"
)

// This independent PNG writer streams grayscale rows, so oversized input
// fixtures do not first allocate an oversized bitmap in the test process.
func grayPNGFixture(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var output bytes.Buffer
	output.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	chunk := func(kind string, data []byte) {
		if err := binary.Write(&output, binary.BigEndian, uint32(len(data))); err != nil {
			t.Fatal(err)
		}
		output.WriteString(kind)
		output.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(data)
		if err := binary.Write(&output, binary.BigEndian, crc.Sum32()); err != nil {
			t.Fatal(err)
		}
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header, width)
	binary.BigEndian.PutUint32(header[4:], height)
	header[8] = 8
	chunk("IHDR", header)
	var compressed bytes.Buffer
	encoder := zlib.NewWriter(&compressed)
	row := make([]byte, int(width)+1)
	for index := uint32(0); index < height; index++ {
		if _, err := encoder.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	chunk("IDAT", compressed.Bytes())
	chunk("IEND", nil)
	return output.Bytes()
}
func TestPNGDimensionAndPixelBoundariesControlOSAdmission(t *testing.T) {
	for _, entry := range []struct {
		name          string
		width, height uint32
		allowed       bool
	}{
		{"one pixel", 1, 1, true}, {"maximum width", 2048, 1, true}, {"width over maximum", 2049, 1, false},
		{"maximum height", 1, 8192, true}, {"height over maximum", 1, 8193, false}, {"exact pixel budget", 2000, 8000, true},
		{"pixel budget exceeded with permitted dimensions", 2048, 7813, false}, {"zero width", 0, 1, false}, {"zero height", 1, 0, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			probe := &exportProbe{status: platform.SaveOutcome{Status: platform.SaveCancelled}}
			request := contracts.PNGRequest{SuggestedFilename: "result.png", DataBase64: base64.StdEncoding.EncodeToString(grayPNGFixture(t, entry.width, entry.height))}
			response, err := exportService(t, probe).SavePNG(context.Background(), request)
			if err != nil || response.OK != entry.allowed {
				t.Fatalf("admission %+v/%v", response, err)
			}
			if entry.allowed {
				if probe.calls.Load() != 1 || response.Data.Status != "cancelled" {
					t.Fatal("valid boundary did not reach OS once")
				}
			} else if probe.calls.Load() != 0 || response.Code != contracts.InvalidInput || response.Data != nil {
				t.Fatal("invalid dimensions crossed OS boundary", response)
			}
		})
	}
}
func TestPNGFilenameByteBoundariesPreserveUnicodeAndRejectOverflow(t *testing.T) {
	base := pngRequest(t)
	for _, entry := range []struct {
		name     string
		filename string
		allowed  bool
	}{
		{"exact bytes", string(bytes.Repeat([]byte{'a'}, 196)) + ".png", true},
		{"one byte over", string(bytes.Repeat([]byte{'a'}, 197)) + ".png", false},
		{"Unicode below boundary", string(bytes.Repeat([]byte("가"), 65)) + ".png", true},
		{"Unicode crosses boundary", string(bytes.Repeat([]byte("가"), 66)) + ".png", false},
		{"uppercase extension", "한글 결과.PNG", true},
	} {
		t.Run(entry.name, func(t *testing.T) {
			probe := &exportProbe{status: platform.SaveOutcome{Status: platform.SaveCancelled}}
			request := base
			request.SuggestedFilename = entry.filename
			response, err := exportService(t, probe).SavePNG(context.Background(), request)
			if err != nil || response.OK != entry.allowed {
				t.Fatal("filename boundary", response, err)
			}
			if entry.allowed != (probe.calls.Load() == 1) {
				t.Fatal("filename side effect", probe.calls.Load())
			}
		})
	}
}
func TestPNGCorruptImageBodyNeverReachesOSDespiteValidHeader(t *testing.T) {
	data := grayPNGFixture(t, 2, 2)
	// The IDAT data begins after signature + 25-byte IHDR + length/type.
	data[41] ^= 1
	probe := &exportProbe{status: platform.SaveOutcome{Status: platform.SaveCancelled}}
	response, err := exportService(t, probe).SavePNG(context.Background(), contracts.PNGRequest{SuggestedFilename: "result.png", DataBase64: base64.StdEncoding.EncodeToString(data)})
	if err != nil || response.OK || response.Code != contracts.InvalidInput || probe.calls.Load() != 0 {
		t.Fatal("corrupt image accepted", response, err)
	}
}
