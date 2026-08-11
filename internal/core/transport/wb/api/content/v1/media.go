package v1

type SaveMediaByLinksRequest struct {
	NMID int64    `json:"nmId"`
	Data []string `json:"data"`
}

type SaveMediaByLinksResponse struct {
	Data EmptyObject `json:"data"`
	ResponseMeta
}
