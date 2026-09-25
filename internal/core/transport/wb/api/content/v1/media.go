package v1

type SaveMediaByLinksRequest struct {
	NMID int64    `json:"nmId"`
	Data []string `json:"data"`
}

type SaveMediaByLinksResponse struct {
	Data EmptyObject `json:"data"`
	ResponseMeta
}

type UploadMediaFileRequest struct {
	NMID        int64
	MediaNumber int
	FileName    string
	Data        []byte
}

type DownloadedMediaFile struct {
	FileName  string
	MediaType string
	Data      []byte
}

type UploadMediaFileResponse struct {
	Data EmptyObject `json:"data"`
	ResponseMeta
}
