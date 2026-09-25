package cardimport_telegram_transport

import (
	"context"
	"errors"
	"time"

	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_service "github.com/ERONIS/wb-service/internal/feature/cardimport/service"

	"go.uber.org/zap"
)

const (
	cardimportRecoveryWorkers      = 2
	cardimportRecoveryScanInterval = 5 * time.Second
	cardimportRecoveryBatchSize    = 1000
	cardimportRecoveryBaseBackoff  = 5 * time.Second
	cardimportRecoveryMaxBackoff   = 5 * time.Minute
	cardimportRecoveryAttemptLimit = 90 * time.Second
	cardimportRefreshDebounce      = 750 * time.Millisecond
)

type cardimportRecoveryKey = cardimport_service.FileID

type cardimportRecoveryJob struct {
	authorTelegramID int64
	chatID           int64
	file             cardimport_service.File
}

type cardimportRecoveryState struct {
	job      cardimportRecoveryJob
	queued   bool
	running  bool
	failures int
	retryAt  time.Time
}

type cardimportSessionRefresh struct {
	authorTelegramID int64
	chatID           int64
	sessionID        cardimport_service.SessionID
}

func (h *Handler) runRecoveryScheduler() {
	ticker := time.NewTicker(cardimportRecoveryScanInterval)
	defer ticker.Stop()

	h.scanRecoverableFiles()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.scanRecoverableFiles()
		case <-h.recoveryWake:
			h.scanRecoverableFiles()
		}
	}
}

func (h *Handler) scanRecoverableFiles() {
	files, err := h.service.ListRecoverableFiles(
		h.ctx,
		time.Now().Add(-staleParsingFileAge),
		h.recoveryStartedAt,
		cardimportRecoveryBatchSize,
	)
	if err != nil {
		if h.ctx.Err() == nil {
			h.logger.Warn("scan recoverable cardimport files failed", zap.Error(err))
		}
		return
	}

	for _, pending := range files {
		h.enqueueRecovery(cardimportRecoveryJob{
			authorTelegramID: pending.AuthorTelegramID,
			chatID:           pending.AuthorTelegramID,
			file:             pending.File,
		}, false)
	}
}

func (h *Handler) wakeRecoveryScheduler() {
	select {
	case h.recoveryWake <- struct{}{}:
	default:
	}
}

func (h *Handler) enqueueRecovery(job cardimportRecoveryJob, force bool) {
	if job.authorTelegramID <= 0 || job.file.ID <= 0 || job.file.SessionID <= 0 {
		return
	}

	now := time.Now()
	h.recoveryMu.Lock()
	state := h.recoveryStates[job.file.ID]
	if state == nil {
		state = &cardimportRecoveryState{}
		h.recoveryStates[job.file.ID] = state
	}
	if job.chatID == 0 {
		job.chatID = state.job.chatID
	}
	state.job = job
	if force {
		state.retryAt = time.Time{}
	}
	if state.queued || state.running || (!force && now.Before(state.retryAt)) {
		h.recoveryMu.Unlock()
		return
	}
	state.queued = true
	h.recoveryMu.Unlock()

	select {
	case h.recoveryJobs <- job.file.ID:
	case <-h.ctx.Done():
		h.markRecoveryDequeued(job.file.ID)
	default:
		h.markRecoveryDequeued(job.file.ID)
		h.wakeRecoveryScheduler()
	}
}

func (h *Handler) markRecoveryDequeued(fileID cardimport_service.FileID) {
	h.recoveryMu.Lock()
	if state := h.recoveryStates[fileID]; state != nil {
		state.queued = false
	}
	h.recoveryMu.Unlock()
}

func (h *Handler) takeRecovery(fileID cardimport_service.FileID) (cardimportRecoveryJob, bool) {
	h.recoveryMu.Lock()
	defer h.recoveryMu.Unlock()
	state := h.recoveryStates[fileID]
	if state == nil || !state.queued || state.running {
		return cardimportRecoveryJob{}, false
	}
	state.queued = false
	state.running = true
	return state.job, true
}

func (h *Handler) finishRecovery(fileID cardimport_service.FileID, err error) {
	h.recoveryMu.Lock()
	defer h.recoveryMu.Unlock()
	state := h.recoveryStates[fileID]
	if state == nil {
		return
	}
	state.running = false
	if err == nil || errors.Is(err, core_errors.ErrNotFound) {
		delete(h.recoveryStates, fileID)
		return
	}
	state.failures++
	state.retryAt = time.Now().Add(cardimportRecoveryBackoff(state.failures))
}

func cardimportRecoveryBackoff(failures int) time.Duration {
	if failures <= 1 {
		return cardimportRecoveryBaseBackoff
	}
	backoff := cardimportRecoveryBaseBackoff
	for attempt := 1; attempt < failures && backoff < cardimportRecoveryMaxBackoff; attempt++ {
		backoff *= 2
	}
	if backoff > cardimportRecoveryMaxBackoff {
		return cardimportRecoveryMaxBackoff
	}
	return backoff
}

func (h *Handler) runRecoveryWorker() {
	for {
		select {
		case <-h.ctx.Done():
			return
		case fileID := <-h.recoveryJobs:
			job, ok := h.takeRecovery(fileID)
			if !ok {
				continue
			}
			h.processRecoveryJob(job)
		}
	}
}

func (h *Handler) processRecoveryJob(job cardimportRecoveryJob) {
	startedAt := time.Now()
	attemptCtx, cancel := context.WithTimeout(h.ctx, cardimportRecoveryAttemptLimit)
	command := cardimport_service.ParseFileCommand{
		AuthorTelegramID: job.authorTelegramID,
		SessionID:        job.file.SessionID,
		FileID:           job.file.ID,
	}
	var err error
	if job.file.Status == cardimport_service.FileStatusInvalid {
		_, err = h.service.ParseFile(attemptCtx, command)
	} else {
		_, err = h.processFile(attemptCtx, job.file, nil, command)
	}
	cancel()
	h.finishRecovery(job.file.ID, err)

	fields := []zap.Field{
		zap.Int64("author_telegram_id", job.authorTelegramID),
		zap.Int64("session_id", int64(job.file.SessionID)),
		zap.Int64("file_id", int64(job.file.ID)),
		zap.Duration("duration", time.Since(startedAt)),
	}
	if err != nil {
		h.logger.Warn(
			"automatic cardimport file recovery will retry",
			append(fields, zap.String("error_class", cardimportRecoveryErrorClass(err)))...,
		)
		return
	}

	h.logger.Info("automatic cardimport file recovery completed", fields...)
	h.scheduleSessionRefresh(cardimportSessionRefresh{
		authorTelegramID: job.authorTelegramID,
		chatID:           job.chatID,
		sessionID:        job.file.SessionID,
	})
}

func cardimportRecoveryErrorClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, core_errors.ErrConflict):
		return "conflict"
	case errors.Is(err, core_errors.ErrInvalidArgument):
		return "invalid_argument"
	default:
		return "transient"
	}
}

func (h *Handler) scheduleSessionRefresh(refresh cardimportSessionRefresh) {
	if refresh.chatID == 0 {
		return
	}
	h.refreshMu.Lock()
	h.pendingRefreshes[refresh.chatID] = refresh
	h.refreshMu.Unlock()
	select {
	case h.refreshWake <- struct{}{}:
	default:
	}
}

func (h *Handler) runRecoveryRefreshes() {
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-h.refreshWake:
		}

		timer := time.NewTimer(cardimportRefreshDebounce)
		select {
		case <-h.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		h.refreshMu.Lock()
		refreshes := make([]cardimportSessionRefresh, 0, len(h.pendingRefreshes))
		for chatID, refresh := range h.pendingRefreshes {
			refreshes = append(refreshes, refresh)
			delete(h.pendingRefreshes, chatID)
		}
		h.refreshMu.Unlock()

		for _, refresh := range refreshes {
			h.refreshSessionMenu(refresh)
		}
	}
}

func (h *Handler) refreshSessionMenu(refresh cardimportSessionRefresh) {
	view, err := h.service.GetView(h.ctx, cardimport_service.SessionCommand{
		AuthorTelegramID: refresh.authorTelegramID,
		SessionID:        refresh.sessionID,
	})
	if err != nil {
		return
	}
	text, markup := h.sessionView(view)
	if err := h.menu.RenderChat(refresh.chatID, text, markup); err != nil && h.ctx.Err() == nil {
		h.logger.Warn(
			"refresh cardimport menu after automatic recovery failed",
			zap.Int64("chat_id", refresh.chatID),
			zap.Int64("session_id", int64(refresh.sessionID)),
			zap.String("error_class", cardimportRecoveryErrorClass(err)),
		)
	}
}
