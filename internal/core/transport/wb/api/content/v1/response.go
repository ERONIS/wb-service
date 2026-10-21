package v1

// ResponseMeta — общие служебные поля JSON-ответов Content API.
type ResponseMeta struct {
	Error            bool   `json:"error"`
	ErrorText        string `json:"errorText"`
	AdditionalErrors any    `json:"additionalErrors"`
}

// DataResponse описывает ответ WB с полем data и служебными полями.
type DataResponse[T any] struct {
	Data T `json:"data"`
	ResponseMeta
}

// EmptyObject принимает как пустой JSON object, так и null.
type EmptyObject struct{}
