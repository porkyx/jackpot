package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"github.com/porkyx/jackpot/internal/contracts"
	"github.com/porkyx/jackpot/internal/dcinside"
	"github.com/porkyx/jackpot/internal/platform"
	"image/png"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxPNGBytes = 25 * 1024 * 1024

type ExportService struct {
	exports platform.Exports
	session contracts.BackendSessionID
	now     func() time.Time
	busy    sync.Mutex
}

func NewExportService(exports platform.Exports, session contracts.BackendSessionID, now func() time.Time) (*ExportService, error) {
	if nilDependency(exports) || now == nil || contracts.ValidateID(session) != nil {
		return nil, contracts.NewFault(contracts.InvalidInput)
	}
	return &ExportService{exports: exports, session: session, now: now}, nil
}
func (service *ExportService) begin(ctx context.Context) (contracts.ResponseHeader, error) {
	if ctx == nil {
		return contracts.ResponseHeader{}, contracts.NewFault(contracts.InvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return contracts.ResponseHeader{}, err
	}
	return contracts.NewResponseHeader(service.session, service.now())
}
func exportResponse(header contracts.ResponseHeader, status string, err error) (contracts.ExportResponse, error) {
	if err != nil {
		return contracts.ExportResponse(failureEnvelope[contracts.ExportData](header, err)), nil
	}
	data := contracts.ExportData{Status: status}
	response := contracts.ExportResponse{ResponseHeader: header, OK: true, Data: &data}
	return response, response.Validate()
}
func validatePNG(request contracts.PNGRequest) ([]byte, error) {
	invalid := func() ([]byte, error) { return nil, contracts.NewFault(contracts.InvalidInput) }
	name := request.SuggestedFilename
	if len(name) == 0 || len(name) > 200 || filepath.Base(name) != name || strings.ContainsAny(name, "/\\:\x00\r\n") || !strings.HasSuffix(strings.ToLower(name), ".png") || !utf8.ValidString(name) {
		return invalid()
	}
	if len(request.DataBase64) == 0 || len(request.DataBase64) > base64.StdEncoding.EncodedLen(maxPNGBytes) {
		return invalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(request.DataBase64)
	if err != nil || len(data) > maxPNGBytes {
		return invalid()
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16000000 {
		return invalid()
	}
	if _, err = png.Decode(bytes.NewReader(data)); err != nil {
		return invalid()
	}
	return data, nil
}
func (service *ExportService) SavePNG(ctx context.Context, request contracts.PNGRequest) (contracts.ExportResponse, error) {
	header, err := service.begin(ctx)
	if err != nil {
		return contracts.ExportResponse{}, err
	}
	if !service.busy.TryLock() {
		return exportResponse(header, "", contracts.NewFault(contracts.InvalidState))
	}
	defer service.busy.Unlock()
	data, err := validatePNG(request)
	request.DataBase64 = ""
	if err != nil {
		return exportResponse(header, "", err)
	}
	result, err := service.exports.SavePNG(ctx, platform.PNGExport{SuggestedFilename: request.SuggestedFilename, Bytes: data})
	if err == nil {
		err = result.Validate()
	}
	if err != nil {
		return exportResponse(header, "", contracts.NewFault(contracts.StorageUnavailable))
	}
	return exportResponse(header, string(result.Status), nil)
}
func (service *ExportService) CopyText(ctx context.Context, request contracts.TextExportRequest) (contracts.ExportResponse, error) {
	header, err := service.begin(ctx)
	if err != nil {
		return contracts.ExportResponse{}, err
	}
	if len(request.Text) == 0 || len(request.Text) > 1024*1024 || strings.ContainsRune(request.Text, 0) || !utf8.ValidString(request.Text) {
		return exportResponse(header, "", contracts.NewFault(contracts.InvalidInput))
	}
	if !service.busy.TryLock() {
		return exportResponse(header, "", contracts.NewFault(contracts.InvalidState))
	}
	defer service.busy.Unlock()
	if err = service.exports.CopyText(ctx, request.Text); err != nil {
		return exportResponse(header, "", contracts.NewFault(contracts.StorageUnavailable))
	}
	return exportResponse(header, "copied", nil)
}
func (service *ExportService) OpenArticle(ctx context.Context, request contracts.ArticleExportRequest) (contracts.ExportResponse, error) {
	header, err := service.begin(ctx)
	if err != nil {
		return contracts.ExportResponse{}, err
	}
	article, err := dcinside.NormalizeArticle(request.URL)
	if err != nil {
		return exportResponse(header, "", contracts.NewFault(contracts.InvalidInput))
	}
	if err = service.exports.OpenArticle(ctx, article.CanonicalURL); err != nil {
		return exportResponse(header, "", contracts.NewFault(contracts.StorageUnavailable))
	}
	return exportResponse(header, "opened", nil)
}
