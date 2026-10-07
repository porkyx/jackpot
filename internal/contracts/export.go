package contracts

type PNGRequest struct {
	SuggestedFilename string `json:"suggestedFilename"`
	DataBase64        string `json:"dataBase64"`
}
type TextExportRequest struct {
	Text string `json:"text"`
}
type ArticleExportRequest struct {
	URL string `json:"url"`
}
type ExportData struct {
	Status string `json:"status"`
}
type ExportResponse Envelope[ExportData]

func (response ExportResponse) Validate() error { return Envelope[ExportData](response).Validate() }
