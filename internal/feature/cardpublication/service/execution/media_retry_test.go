package execution

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type uncertainMediaError struct{}

func (uncertainMediaError) Error() string {
	return "неизвестный результат отправки"
}
func (uncertainMediaError) Code() string                    { return core_wb.ErrorCodeTransport }
func (uncertainMediaError) Delivery() core_wb.DeliveryState { return core_wb.UnknownDelivery }
func (uncertainMediaError) HTTPStatus() int                 { return 0 }
func (uncertainMediaError) RetryAfter() time.Duration       { return 0 }

type uncertainMediaTransport struct {
	CatalogTransport
	uploads, downloads int
}

func (transport *uncertainMediaTransport) SaveMediaByLinks(context.Context, CabinetID, domain.ClientGeneration, contentapi.SaveMediaByLinksRequest) (contentapi.SaveMediaByLinksResponse, error) {
	transport.uploads++
	return contentapi.SaveMediaByLinksResponse{}, uncertainMediaError{}
}
func (transport *uncertainMediaTransport) DownloadMediaFile(context.Context, string) (contentapi.DownloadedMediaFile, error) {
	transport.downloads++
	if transport.downloads == 1 {
		return contentapi.DownloadedMediaFile{}, io.ErrUnexpectedEOF
	}
	return contentapi.DownloadedMediaFile{FileName: "image.jpg", MediaType: "image/jpeg", Data: []byte{1}}, nil
}
func (transport *uncertainMediaTransport) UploadMediaFile(context.Context, CabinetID, domain.ClientGeneration, contentapi.UploadMediaFileRequest) (contentapi.UploadMediaFileResponse, error) {
	transport.uploads++
	return contentapi.UploadMediaFileResponse{}, uncertainMediaError{}
}

func TestMediaMutationsPreserveUnknownDeliveryWithoutRetry(t *testing.T) {
	request := contentapi.SaveMediaByLinksRequest{NMID: 1, Data: []string{"https://example.test/photo.jpg"}}
	links := &uncertainMediaTransport{}
	result := executeMediaMutation(context.Background(), links, "cabinet", domain.NewClientGeneration("token"), request, MediaUploadByLinks)
	if links.uploads != 1 || result.Disposition != SubmissionUncertain || result.Delivery != SubmissionUnknownDelivery {
		t.Fatalf("повтор или потеря неопределённости: uploads=%d result=%+v", links.uploads, result)
	}
	files := &uncertainMediaTransport{}
	waits := 0
	result = executeDirectMediaMutationWithWait(context.Background(), files, "cabinet", domain.NewClientGeneration("token"), request, func(context.Context, time.Duration) error { waits++; return nil })
	if files.downloads != 2 || waits != 1 || files.uploads != 1 || result.Disposition != SubmissionUncertain {
		t.Fatalf("некорректные повторы: uploads=%d downloads=%d waits=%d result=%+v", files.uploads, files.downloads, waits, result)
	}
}
