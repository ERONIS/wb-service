package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	transfer "github.com/ERONIS/wb-service/internal/feature/transfer/service"
)

func TestMediaVisibilityResumesAfterRestartWithoutResubmission(t *testing.T) {
	repo := &mediaRepositoryStub{action: validMediaAction()}
	sender := &mediaTransportStub{stable: true}
	if err := mediaTestDispatcher(repo, sender).dispatch(context.Background(), repo.action.ProductActionCandidate); err != nil {
		t.Fatal(err)
	}
	deadline := repo.job.DeadlineAt
	// A new dispatcher has no cache, in-flight map or state from the sender.
	checker := &mediaTransportStub{stable: true}
	restarted := mediaTestDispatcher(repo, checker)
	restarted.concurrency = 1
	if err := restarted.ProcessMediaVisibility(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !repo.updated || repo.update.OutcomeClass != "" || repo.update.NextDelay <= 0 || repo.action.State != "reconciling" {
		t.Fatalf("missing media did not remain pending: %+v", repo.update)
	}
	if repo.job.DeadlineAt != deadline || checker.reads != 1 || checker.mutations != 0 {
		t.Fatal("restart reset deadline or resubmitted media")
	}
	checker.photos = []contentapi.CardPhoto{{Big: "https://example.test/visible.jpg"}}
	if err := restarted.ProcessMediaVisibility(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.update.OutcomeClass != transfer.ResultSuccess || checker.reads != 2 || checker.mutations != 0 {
		t.Fatalf("fresh visibility was not confirmed: %+v", repo.update)
	}
	if repo.result.Disposition != SubmissionAccepted || repo.result.OutcomeCode != "WB_MEDIA_REQUEST_ACCEPTED" {
		t.Fatal("visibility rewrote HTTP submission evidence")
	}
}

func TestMediaVisibilityTimeoutUsesPersistedDeadline(t *testing.T) {
	repo := &mediaRepositoryStub{action: validMediaAction()}
	transport := &mediaTransportStub{stable: true}
	d := mediaTestDispatcher(repo, transport)
	if err := d.dispatch(context.Background(), repo.action.ProductActionCandidate); err != nil {
		t.Fatal(err)
	}
	repo.job.DeadlineAt = time.Now().Add(-time.Second)
	if err := d.checkMediaVisibility(context.Background(), repo.job); err != nil {
		t.Fatal(err)
	}
	if repo.update.OutcomeClass != transfer.ResultUnresolved || repo.update.OutcomeCode != "WB_MEDIA_VISIBILITY_TIMEOUT" || transport.reads != 1 {
		t.Fatalf("expired job sent a WB request or lost timeout: %+v", repo.update)
	}
	if repo.result.Disposition != SubmissionAccepted {
		t.Fatal("timeout rewrote accepted HTTP attempt")
	}
}

func TestMediaVisibilityTransientFailureReschedulesAndCancellationKeepsJob(t *testing.T) {
	repo := &mediaRepositoryStub{action: validMediaAction()}
	transport := &mediaTransportStub{stable: true}
	d := mediaTestDispatcher(repo, transport)
	if err := d.dispatch(context.Background(), repo.action.ProductActionCandidate); err != nil {
		t.Fatal(err)
	}
	transport.readErr = errors.New("WB unavailable")
	if err := d.checkMediaVisibility(context.Background(), repo.job); err != nil {
		t.Fatal(err)
	}
	if repo.update.OutcomeClass != "" || repo.update.ErrorCode == "" {
		t.Fatalf("transient error became terminal: %+v", repo.update)
	}
	repo.updated = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.checkMediaVisibility(ctx, repo.job); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if repo.updated || repo.action.State != "reconciling" {
		t.Fatal("shutdown finalized a pending job")
	}
}

func TestMediaVisibilityChecksNoMoreOftenThanOncePerMinute(t *testing.T) {
	for _, test := range []struct {
		checks int
		want   time.Duration
	}{
		{0, time.Minute}, {1, time.Minute}, {3, time.Minute}, {10000, time.Minute},
	} {
		if got := mediaVisibilityDelay(3*time.Second, test.checks); got != test.want {
			t.Fatalf("check %d: %v", test.checks, got)
		}
	}
}

func TestUncertainMediaResultIsVerifiedBeforeRequiringAttention(t *testing.T) {
	for _, cause := range []MediaMutationResult{
		classifyMediaError(responseError{429}),
		classifyMediaError(unknownMediaError{}),
		partialMediaMutationResult(),
	} {
		t.Run(cause.OutcomeCode, func(t *testing.T) {
			repo := &mediaRepositoryStub{action: validMediaAction()}
			transport := &mediaTransportStub{stable: true}
			d := mediaTestDispatcher(repo, transport)
			if err := d.dispatch(context.Background(), repo.action.ProductActionCandidate); err != nil {
				t.Fatal(err)
			}
			// Simulate the same committed attempt finishing without a reliable
			// response. Its HTTP evidence remains uncertain even if photos appear.
			repo.action.State = "dispatching"
			ctx, cancel := publicationResultContext(context.Background())
			defer cancel()
			if err := d.recordResult(ctx, repo.action.ProductActionCandidate, mediaAttemptFromAction(repo.action), cause); err != nil {
				t.Fatal(err)
			}
			if repo.action.State != "reconciling" || repo.job.AttemptID == 0 {
				t.Fatal("uncertain upload became terminal instead of scheduling verification")
			}
			if err := d.checkMediaVisibility(context.Background(), repo.job); err != nil {
				t.Fatal(err)
			}
			if repo.update.OutcomeClass != "" {
				t.Fatalf("missing photos prematurely finalized: %+v", repo.update)
			}
			transport.photos = []contentapi.CardPhoto{{Big: "https://example.test/photo.jpg"}}
			if err := d.checkMediaVisibility(context.Background(), repo.job); err != nil {
				t.Fatal(err)
			}
			if repo.update.OutcomeClass != transfer.ResultSuccess || repo.result != cause || transport.mutations != 1 {
				t.Fatalf("verification rewrote HTTP evidence or resent media: result=%+v update=%+v", repo.result, repo.update)
			}
		})
	}
}
