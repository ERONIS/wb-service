package cardimport_telegram_transport

import (
	"context"
	"errors"
	"testing"
	"time"

	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"
)

func TestRecoveryQueueDeduplicatesAndForceBypassesBackoff(t *testing.T) {
	t.Parallel()

	handler := &Handler{
		ctx:            context.Background(),
		recoveryWake:   make(chan struct{}, 1),
		recoveryJobs:   make(chan cardimportRecoveryKey, 4),
		recoveryStates: make(map[cardimport_service.FileID]*cardimportRecoveryState),
	}
	job := cardimportRecoveryJob{
		authorTelegramID: 7,
		file: cardimport_service.File{
			ID:        11,
			SessionID: 13,
			Status:    cardimport_service.FileStatusReserved,
		},
	}

	handler.enqueueRecovery(job, false)
	handler.enqueueRecovery(job, false)
	if got := len(handler.recoveryJobs); got != 1 {
		t.Fatalf("queued jobs = %d, want 1", got)
	}

	fileID := <-handler.recoveryJobs
	if _, ok := handler.takeRecovery(fileID); !ok {
		t.Fatal("takeRecovery() did not return queued job")
	}
	handler.enqueueRecovery(job, true)
	if got := len(handler.recoveryJobs); got != 0 {
		t.Fatalf("job queued while already running: %d", got)
	}

	handler.finishRecovery(fileID, errors.New("temporary failure"))
	handler.enqueueRecovery(job, false)
	if got := len(handler.recoveryJobs); got != 0 {
		t.Fatalf("job queued during backoff: %d", got)
	}
	handler.enqueueRecovery(job, true)
	if got := len(handler.recoveryJobs); got != 1 {
		t.Fatalf("forced queued jobs = %d, want 1", got)
	}
}

func TestCardimportRecoveryBackoffIsBounded(t *testing.T) {
	t.Parallel()

	if got := cardimportRecoveryBackoff(1); got != cardimportRecoveryBaseBackoff {
		t.Fatalf("first backoff = %s", got)
	}
	if got := cardimportRecoveryBackoff(100); got != cardimportRecoveryMaxBackoff {
		t.Fatalf("maximum backoff = %s", got)
	}
}

func TestUnfinishedFilesCountIncludesActiveParsing(t *testing.T) {
	t.Parallel()

	view := cardimport_service.SessionView{Files: []cardimport_service.FileView{
		{File: cardimport_service.File{Status: cardimport_service.FileStatusReserved}},
		{File: cardimport_service.File{Status: cardimport_service.FileStatusStored}},
		{File: cardimport_service.File{
			Status:    cardimport_service.FileStatusParsing,
			UpdatedAt: time.Now(),
		}},
		{File: cardimport_service.File{Status: cardimport_service.FileStatusValid}},
	}}

	if got := unfinishedFilesCount(view); got != 3 {
		t.Fatalf("unfinishedFilesCount() = %d, want 3", got)
	}
}
