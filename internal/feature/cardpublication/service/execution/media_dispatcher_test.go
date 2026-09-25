package execution

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	tx "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/catalog"
	"github.com/ERONIS/wb-service/internal/feature/cardpublication/service/monitoring"
	transfer "github.com/ERONIS/wb-service/internal/feature/transfer/service"
	"go.uber.org/zap"
)

type testDBTX struct{ tx.DBTX }
type contextCheckingUOW struct{}

func (contextCheckingUOW) WithinTransaction(ctx context.Context, fn func(context.Context, tx.DBTX) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(ctx, testDBTX{}); err != nil {
		return err
	}
	return ctx.Err()
}

type mediaRepositoryStub struct {
	MediaJournalRepository
	action             MediaAction
	candidates         []MediaActionCandidate
	finished, recorded bool
	finishErr          error
	result             MediaMutationResult
	job                MediaVisibilityJob
	update             MediaVisibilityUpdate
	updated            bool
}

func (s *mediaRepositoryStub) ListDispatchableMediaActions(context.Context, int) ([]MediaActionCandidate, error) {
	return append([]MediaActionCandidate(nil), s.candidates...), nil
}
func (s *mediaRepositoryStub) ListPendingMediaActions(context.Context, int) ([]MediaActionCandidate, error) {
	return append([]MediaActionCandidate(nil), s.candidates...), nil
}
func (s *mediaRepositoryStub) ListInterruptedMediaActions(context.Context, int) ([]MediaActionCandidate, error) {
	return nil, nil
}

func (s *mediaRepositoryStub) LockMediaAction(context.Context, tx.DBTX, MediaActionCandidate) (MediaAction, error) {
	return s.action, nil
}
func (s *mediaRepositoryStub) InsertActionObservation(context.Context, tx.DBTX, transfer.TransferID, string, ObservationDraft) (int64, error) {
	return 1, nil
}
func (s *mediaRepositoryStub) FinishMediaWithoutAttempt(context.Context, tx.DBTX, MediaAction, string) (MediaGroupResult, error) {
	s.finished = true
	return MediaGroupResult{GroupTargetID: 1}, s.finishErr
}
func (s *mediaRepositoryStub) PublicationPlanTerminal(context.Context, tx.DBTX, transfer.TransferID, int64) (bool, error) {
	return false, nil
}
func (s *mediaRepositoryStub) BeginMediaAttempt(_ context.Context, _ tx.DBTX, c BeginMediaAttemptCommand) (MediaAttempt, error) {
	s.action.State = "dispatching"
	s.action.AttemptID = 1
	s.action.AttemptRequestPayload = c.RequestPayload
	s.action.AttemptRequestDigest = c.RequestDigest
	s.action.AttemptStartedAt = time.Now()
	s.action.RecheckObservationID = 1
	return mediaAttemptFromAction(s.action), nil
}
func (s *mediaRepositoryStub) RecordMediaResult(ctx context.Context, _ tx.DBTX, _ MediaAction, _ MediaAttempt, result MediaMutationResult) (MediaGroupResult, error) {
	if err := ctx.Err(); err != nil {
		return MediaGroupResult{}, err
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > publicationResultTimeout {
		return MediaGroupResult{}, errors.New("unbounded result write")
	}
	s.recorded = true
	s.result = result
	return MediaGroupResult{GroupTargetID: 1}, nil
}

func (s *mediaRepositoryStub) ScheduleMediaVisibility(ctx context.Context, dbtx tx.DBTX, action MediaAction, attempt MediaAttempt, result MediaMutationResult, interval, timeout time.Duration) error {
	if _, err := s.RecordMediaResult(ctx, dbtx, action, attempt, result); err != nil {
		return err
	}
	s.action.State = "reconciling"
	s.job = MediaVisibilityJob{Candidate: action.ProductActionCandidate, AttemptID: attempt.ID, Revision: 1,
		DeadlineAt: time.Now().Add(timeout), CheckCount: 1}
	return nil
}

func (s *mediaRepositoryStub) ClaimMediaVisibility(context.Context, int, time.Duration) ([]MediaVisibilityJob, error) {
	return []MediaVisibilityJob{s.job}, nil
}

func (s *mediaRepositoryStub) UpdateMediaVisibility(_ context.Context, _ tx.DBTX, _ MediaAction, job MediaVisibilityJob, update MediaVisibilityUpdate) (MediaGroupResult, error) {
	if job.Revision != s.job.Revision {
		return MediaGroupResult{}, ErrMediaActionConflict
	}
	s.updated, s.update = true, update
	if update.OutcomeClass != "" {
		s.action.State = "terminal"
	}
	return MediaGroupResult{GroupTargetID: 1}, nil
}

type mediaTransportStub struct {
	CatalogTransport
	stable            bool
	cancel            context.CancelFunc
	mutationStarted   chan struct{}
	mutations, reads  int
	photos            []contentapi.CardPhoto
	readErr           error
	downloads         map[string]contentapi.DownloadedMediaFile
	uploads           []contentapi.UploadMediaFileRequest
	directUploadErrAt int
}

func (s *mediaTransportStub) CardsList(ctx context.Context, _ CabinetID, _ contentapi.CardsListQuery, _ contentapi.CardsListRequest) (contentapi.CardsListResponse, error) {
	s.reads++
	if err := ctx.Err(); err != nil {
		return contentapi.CardsListResponse{}, err
	}
	if !s.stable {
		return contentapi.CardsListResponse{}, s.readErr
	}
	return contentapi.CardsListResponse{Cards: []contentapi.Card{{NMID: 10, VendorCode: "SKU", Photos: s.photos}}}, s.readErr
}
func (s *mediaTransportStub) SaveMediaByLinks(ctx context.Context, _ CabinetID, _ domain.ClientGeneration, _ contentapi.SaveMediaByLinksRequest) (contentapi.SaveMediaByLinksResponse, error) {
	if err := ctx.Err(); err != nil {
		return contentapi.SaveMediaByLinksResponse{}, err
	}
	s.mutations++
	if s.mutationStarted != nil {
		select {
		case s.mutationStarted <- struct{}{}:
		default:
		}
	}
	if s.cancel != nil {
		s.cancel()
	}
	return contentapi.SaveMediaByLinksResponse{}, nil
}

func (s *mediaTransportStub) DownloadMediaFile(ctx context.Context, link string) (contentapi.DownloadedMediaFile, error) {
	if err := ctx.Err(); err != nil {
		return contentapi.DownloadedMediaFile{}, err
	}
	file, ok := s.downloads[link]
	if !ok {
		return contentapi.DownloadedMediaFile{}, errors.New("media not found")
	}
	return file, nil
}

func (s *mediaTransportStub) UploadMediaFile(ctx context.Context, _ CabinetID, _ domain.ClientGeneration, request contentapi.UploadMediaFileRequest) (contentapi.UploadMediaFileResponse, error) {
	if err := ctx.Err(); err != nil {
		return contentapi.UploadMediaFileResponse{}, err
	}
	s.uploads = append(s.uploads, request)
	if s.directUploadErrAt == len(s.uploads) {
		return contentapi.UploadMediaFileResponse{}, errors.New("upload failed")
	}
	return contentapi.UploadMediaFileResponse{}, nil
}

func TestExecuteMediaMutationUploadsFilesInPhotoOrder(t *testing.T) {
	transport := &mediaTransportStub{downloads: map[string]contentapi.DownloadedMediaFile{
		"photo-1": {FileName: "one.jpg", MediaType: "image/jpeg", Data: []byte{1}},
		"video":   {FileName: "clip.mp4", MediaType: "video/mp4", Data: []byte{2}},
		"photo-2": {FileName: "two.png", MediaType: "image/png", Data: []byte{3}},
	}}
	result := executeMediaMutation(context.Background(), transport, "one", domain.ClientGeneration{},
		contentapi.SaveMediaByLinksRequest{NMID: 10, Data: []string{"photo-1", "video", "photo-2"}},
		MediaUploadByFile)
	if result.Disposition != SubmissionAccepted || len(transport.uploads) != 3 {
		t.Fatalf("result=%+v uploads=%d", result, len(transport.uploads))
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("invalid accepted result: %v", err)
	}
	got := []int{transport.uploads[0].MediaNumber, transport.uploads[1].MediaNumber, transport.uploads[2].MediaNumber}
	want := []int{1, 1, 2}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("media numbers = %v, want %v", got, want)
		}
	}
}

func TestExecuteMediaMutationMarksPartialDirectUploadUncertain(t *testing.T) {
	transport := &mediaTransportStub{
		directUploadErrAt: 2,
		downloads: map[string]contentapi.DownloadedMediaFile{
			"one": {FileName: "one.jpg", MediaType: "image/jpeg", Data: []byte{1}},
			"two": {FileName: "two.jpg", MediaType: "image/jpeg", Data: []byte{2}},
		},
	}
	result := executeMediaMutation(context.Background(), transport, "one", domain.ClientGeneration{},
		contentapi.SaveMediaByLinksRequest{NMID: 10, Data: []string{"one", "two"}}, MediaUploadByFile)
	if result.Disposition != SubmissionUncertain || result.OutcomeCode != "WB_MEDIA_PARTIAL_UPLOAD" {
		t.Fatalf("result=%+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("invalid partial result: %v", err)
	}
}

type executionResultsStub struct {
	PublicationExecutionResultApplier
}

func (executionResultsStub) BeginMediaWithin(context.Context, tx.DBTX, transfer.TransferID) error {
	return nil
}

type authorizationStub struct{ LiveAuthorizationVerifier }

func (authorizationStub) LockValid(context.Context, tx.DBTX, transfer.LiveAuthorizationCheck) (transfer.LiveAuthorizationEvidence, error) {
	return transfer.LiveAuthorizationEvidence{}, nil
}

type baselineStub struct {
	monitoring.ErrorFeedRepository
	monitoring.ErrorTargetSource
}

func (baselineStub) CaptureErrorBaseline(context.Context, tx.DBTX, monitoring.CaptureErrorBaselineCommand) (monitoring.ErrorBaseline, error) {
	return monitoring.ErrorBaseline{}, nil
}

func validMediaAction() MediaAction {
	payload := []byte(`{"links":["https://example.test/photo.jpg"]}`)
	member := ProductActionMember{ID: 1, GroupTargetID: 1, TransferItemTargetID: 1, VendorCode: "SKU"}
	return MediaAction{
		ProductActionCandidate: ProductActionCandidate{TransferID: 1, ActionID: 1, TargetID: 1, CabinetID: "one", AuthorizationID: 1, PlanDigest: Digest{1}, TargetSetRoot: Digest{2}},
		PlanID:                 1, State: "planned", RequestPayload: payload,
		RequestDigest:    digestParts("cardpublication-media-template:v1", payload),
		MediaLinkSetRoot: digestParts("cardpublication-media-links:v1", payload),
		MemberSetDigest:  digestMembers([]ActionMemberDraft{{GroupTargetID: 1, TransferItemTargetID: 1, VendorCode: "SKU"}}),
		SellerKey:        domain.SellerKey{1}, ClientGeneration: domain.ClientGeneration{1},
		CredentialExpiresAt: time.Now().Add(time.Hour), PreflightObservationID: 1, Member: member, AttributionID: 1, NMID: 10,
	}
}
func mediaTestDispatcher(repo *mediaRepositoryStub, transport *mediaTransportStub) *MediaDispatcher {
	uow := contextCheckingUOW{}
	baseline := baselineStub{}
	return &MediaDispatcher{repository: repo, transport: transport, catalogReader: catalog.NewCatalogReader(transport),
		errorFeed: monitoring.NewErrorFeed(transport, baseline, baseline, uow), authorization: authorizationStub{},
		transferResults: executionResultsStub{}, uow: uow, logger: zap.NewNop(), mediaCheckInterval: time.Millisecond, mediaCheckTimeout: time.Second}
}

func TestMediaSubmissionReleasesSlotBeforeVisibility(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &mediaRepositoryStub{action: validMediaAction()}
	repo.candidates = []MediaActionCandidate{repo.action.ProductActionCandidate}
	transport := &mediaTransportStub{
		stable:          true,
		mutationStarted: make(chan struct{}, 1),
	}
	dispatcher := mediaTestDispatcher(repo, transport)
	dispatcher.enabled = true
	dispatcher.concurrency = 1
	dispatcher.inFlight = make(map[mediaActionKey]struct{})
	dispatcher.asyncErrors = make(chan error, 32)

	startedAt := time.Now()
	if err := dispatcher.ProcessPending(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(startedAt) > 100*time.Millisecond {
		t.Fatal("polling waited for media visibility")
	}
	select {
	case <-transport.mutationStarted:
	case <-time.After(time.Second):
		t.Fatal("media mutation did not start")
	}
	dispatcher.Wait()
	if dispatcher.mediaActionsInFlight() != 0 || !repo.recorded || repo.action.State != "reconciling" || repo.job.AttemptID == 0 {
		t.Fatalf("submission did not release slot with durable check: %+v", repo)
	}
	if transport.reads != 1 || !dispatcher.reserveMediaAction(MediaActionCandidate{TransferID: 2, ActionID: 2}) {
		t.Fatal("visibility held a send slot or performed an extra WB request")
	}
}

func TestMediaRecheckFinishesWithoutSpuriousConflict(t *testing.T) {
	repo := &mediaRepositoryStub{action: validMediaAction()}
	if err := repo.action.Validate(); err != nil {
		t.Fatal(err)
	}
	transport := &mediaTransportStub{}
	dispatcher := mediaTestDispatcher(repo, transport)
	if err := dispatcher.dispatch(context.Background(), repo.action.ProductActionCandidate); err != nil {
		t.Fatal(err)
	}
	if !repo.finished || repo.recorded || transport.mutations != 0 {
		t.Fatalf("unexpected dispatch: %+v %+v", repo, transport)
	}
	repo.finishErr = errors.New("write failed")
	if err := dispatcher.dispatch(context.Background(), repo.action.ProductActionCandidate); !errors.Is(err, repo.finishErr) {
		t.Fatalf("write failure hidden: %v", err)
	}
}
func TestMediaResultIsPersistedAfterShutdownCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &mediaRepositoryStub{action: validMediaAction()}
	transport := &mediaTransportStub{stable: true, cancel: cancel}
	dispatcher := mediaTestDispatcher(repo, transport)
	if err := dispatcher.dispatch(ctx, repo.action.ProductActionCandidate); err != nil {
		t.Fatal(err)
	}
	if !repo.recorded || repo.result.HTTPStatus != 200 || repo.result.OutcomeClass != transfer.ResultSuccess || repo.action.State != "reconciling" || repo.job.AttemptID == 0 {
		t.Fatalf("response evidence lost: %+v", repo.result)
	}
	if transport.mutations != 1 || transport.reads != 1 {
		t.Fatalf("requests continued after shutdown: %+v", transport)
	}
}

type responseError struct{ status int }

func (e responseError) Error() string                   { return "WB rejected request" }
func (e responseError) Code() string                    { return "unexpected_status" }
func (e responseError) Delivery() core_wb.DeliveryState { return core_wb.ResponseReceived }
func (e responseError) HTTPStatus() int                 { return e.status }
func (e responseError) RetryAfter() time.Duration       { return 0 }
func TestPublicationClassifiersPreserveResponseStatus(t *testing.T) {
	for _, status := range []int{400, 429, 500} {
		err := fmt.Errorf("wrapped: %w", responseError{status})
		product := classifySubmissionError(ProductAction{Members: []ProductActionMember{{ID: 1}}}, err)
		media := classifyMediaError(err)
		if product.HTTPStatus != status || media.HTTPStatus != status {
			t.Fatalf("HTTP %d lost: %+v %+v", status, product, media)
		}
		if err := ValidateSubmissionResult(product); err != nil {
			t.Fatal(err)
		}
		if err := media.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMediaActionValidateAcceptsZeroAttributionID(t *testing.T) {
	action := validMediaAction()
	action.AttributionID = 0
	if err := action.Validate(); err != nil {
		t.Fatalf("expected action with AttributionID=0 to be valid, got: %v", err)
	}

	action.AttributionID = -1
	if err := action.Validate(); err == nil {
		t.Fatal("expected action with AttributionID=-1 to be invalid")
	}
}
