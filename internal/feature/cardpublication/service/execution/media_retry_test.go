package execution

import (
	"context"
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
	uploads int
}

func (transport *uncertainMediaTransport) SaveMediaByLinks(context.Context, SellerID, domain.ClientGeneration, contentapi.SaveMediaByLinksRequest) (contentapi.SaveMediaByLinksResponse, error) {
	transport.uploads++
	return contentapi.SaveMediaByLinksResponse{}, uncertainMediaError{}
}
func TestMediaMutationsPreserveUnknownDeliveryWithoutRetry(t *testing.T) {
	request := contentapi.SaveMediaByLinksRequest{NMID: 1, Data: []string{"https://example.test/photo.jpg"}}
	links := &uncertainMediaTransport{}
	result := executeMediaMutation(context.Background(), links, "cabinet", domain.NewClientGeneration("token"), request)
	if links.uploads != 1 || result.Disposition != SubmissionUncertain || result.Delivery != SubmissionUnknownDelivery {
		t.Fatalf("повтор или потеря неопределённости: uploads=%d result=%+v", links.uploads, result)
	}
}
