package v1

// ResponseMeta — общие служебные поля JSON-ответов Content API.
type ResponseMeta struct {
	Error            bool   `json:"error"`
	ErrorText        string `json:"errorText"`
	AdditionalErrors any    `json:"additionalErrors"`
}

// EmptyObject принимает как пустой JSON object, так и null.
type EmptyObject struct{}

// APIErrorResponse описывает общий формат HTTP-ошибки WB API.
type APIErrorResponse struct {
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Code       string `json:"code"`
	RequestID  string `json:"requestId"`
	Origin     string `json:"origin"`
	Status     int    `json:"status"`
	StatusText string `json:"statusText"`
	Timestamp  string `json:"timestamp"`
	Details    any    `json:"details"`
}
