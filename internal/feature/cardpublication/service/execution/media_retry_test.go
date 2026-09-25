package execution

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type retryResponseError struct {
	responseError
	delay time.Duration
}

func (e retryResponseError) RetryAfter() time.Duration { return e.delay }

type unknownMediaError struct{}

func (unknownMediaError) Error() string                   { return "response lost" }
func (unknownMediaError) Code() string                    { return "transport_error" }
func (unknownMediaError) Delivery() core_wb.DeliveryState { return core_wb.UnknownDelivery }
func (unknownMediaError) HTTPStatus() int                 { return 0 }
func (unknownMediaError) RetryAfter() time.Duration       { return 0 }

type retryingFileTransport struct {
	mediaTransportStub
	failures map[int]error
}

func (s *retryingFileTransport) UploadMediaFile(_ context.Context, _ CabinetID, _ domain.ClientGeneration,
	request contentapi.UploadMediaFileRequest,
) (contentapi.UploadMediaFileResponse, error) {
	s.uploads = append(s.uploads, request)
	return contentapi.UploadMediaFileResponse{}, s.failures[len(s.uploads)]
}

func TestDirectMediaRetryContinuesAtFailedFile(t *testing.T) {
	for _, failure := range []error{
		retryResponseError{responseError{429}, 37 * time.Second},
		responseError{503},
		unknownMediaError{},
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			transport := &retryingFileTransport{
				mediaTransportStub: mediaTransportStub{downloads: map[string]contentapi.DownloadedMediaFile{
					"first":  {FileName: "first.jpg", MediaType: "image/jpeg", Data: []byte{1}},
					"second": {FileName: "second.jpg", MediaType: "image/jpeg", Data: []byte{2}},
					"third":  {FileName: "third.jpg", MediaType: "image/jpeg", Data: []byte{3}},
				}},
				failures: map[int]error{2: failure},
			}
			var waits []time.Duration
			result := executeDirectMediaMutationWithWait(context.Background(), transport, "one", domain.ClientGeneration{},
				contentapi.SaveMediaByLinksRequest{NMID: 10, Data: []string{"first", "second", "third"}},
				func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil })
			if result.Disposition != SubmissionAccepted || result.Validate() != nil || len(transport.uploads) != 4 || len(waits) != 1 {
				t.Fatalf("result=%+v uploads=%v waits=%v", result, transport.uploads, waits)
			}
			var numbers []int
			for _, upload := range transport.uploads {
				numbers = append(numbers, upload.MediaNumber)
			}
			if !reflect.DeepEqual(numbers, []int{1, 2, 2, 3}) || !reflect.DeepEqual(transport.uploads[1], transport.uploads[2]) {
				t.Fatalf("retry changed file/slot or resent completed file: %+v", transport.uploads)
			}
			if limited, ok := failure.(retryResponseError); ok && waits[0] < limited.delay {
				t.Fatalf("Retry-After ignored: %v", waits)
			}
		})
	}
}

func TestMediaRetryIsBoundedAndDoesNotRetryPermanentErrors(t *testing.T) {
	for _, test := range []struct{ status, calls int }{
		{429, mediaRequestAttempts}, {503, mediaRequestAttempts},
		{400, 1}, {401, 1}, {403, 1}, {404, 1},
	} {
		calls, waits := 0, 0
		_, err := retryMediaRequest(context.Background(), func(context.Context, time.Duration) error { waits++; return nil },
			func() (int, error) { calls++; return 0, responseError{test.status} })
		if err == nil || calls != test.calls || waits != test.calls-1 {
			t.Fatalf("HTTP %d: calls=%d waits=%d err=%v", test.status, calls, waits, err)
		}
	}
}

func TestMediaRetryCancellationPreservesLastResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	_, err := retryMediaRequest(ctx, func(ctx context.Context, _ time.Duration) error {
		cancel()
		return waitMediaRetry(ctx, time.Hour)
	}, func() (int, error) { calls++; return 0, responseError{429} })
	var classified core_wb.ClassifiedError
	if calls != 1 || !errors.Is(err, context.Canceled) || !errors.As(err, &classified) || classified.HTTPStatus() != 429 {
		t.Fatalf("cancellation lost response evidence or sent more requests: calls=%d err=%v", calls, err)
	}
}

func TestMediaRetryKeepsEarlierDeliveryIfRetryCannotStart(t *testing.T) {
	calls := 0
	_, err := retryMediaRequest(context.Background(), func(context.Context, time.Duration) error { return nil },
		func() (int, error) {
			calls++
			if calls == 1 {
				return 0, unknownMediaError{}
			}
			return 0, errors.New("credentials unavailable before next dispatch")
		})
	result := classifyMediaError(err)
	if calls != 2 || result.Delivery != SubmissionUnknownDelivery || result.Disposition != SubmissionUncertain {
		t.Fatalf("earlier dispatch was incorrectly treated as unsent: calls=%d result=%+v", calls, result)
	}
}
