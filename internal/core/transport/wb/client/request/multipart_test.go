package request_test

import (
	"context"
	"io"
	"net/url"
	"testing"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	request "github.com/ERONIS/wb-service/internal/core/transport/wb/client/request"
)

func TestPrepareMultipartMediaFile(t *testing.T) {
	baseURL, err := url.Parse("https://content-api.wildberries.ru")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := contentapi.UploadMediaFileOperation(123, 2)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := request.Prepare(baseURL, operation, nil, request.MultipartFile{
		FieldName: "uploadfile",
		FileName:  "photo.jpg",
		Data:      []byte("image-data"),
	})
	if err != nil {
		t.Fatal(err)
	}
	httpRequest, err := prepared.NewHTTPRequest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := httpRequest.Header.Get("X-Nm-Id"); got != "123" {
		t.Fatalf("X-Nm-Id = %q", got)
	}
	if got := httpRequest.Header.Get("X-Photo-Number"); got != "2" {
		t.Fatalf("X-Photo-Number = %q", got)
	}
	reader, err := httpRequest.MultipartReader()
	if err != nil {
		t.Fatal(err)
	}
	part, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if part.FormName() != "uploadfile" || part.FileName() != "photo.jpg" {
		t.Fatalf("unexpected multipart part: field=%q file=%q", part.FormName(), part.FileName())
	}
	data, err := io.ReadAll(part)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-data" {
		t.Fatalf("multipart data = %q", data)
	}
}
