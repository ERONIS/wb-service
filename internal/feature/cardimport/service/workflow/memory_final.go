package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
)

func (store *sessionStore) prepareFinalization(ctx context.Context, user domain.User, command FinalizeCommand) (FinalizedSession, BatchHeader, error) {
	if err := ctx.Err(); err != nil {
		return FinalizedSession{}, BatchHeader{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(user.TelegramID, command.SessionID)
	if err != nil {
		return FinalizedSession{}, BatchHeader{}, err
	}
	digest := command.digest()
	if session.Status == SessionStatusFinalized {
		if session.command != digest {
			return FinalizedSession{}, BatchHeader{}, fmt.Errorf("cardimport finalize command changed: %w", core_errors.ErrConflict)
		}
		return FinalizedSession{}, session.batch, nil
	}
	if session.Status != SessionStatusCollecting || session.finalizing || session.Revision != command.ExpectedRevision {
		return FinalizedSession{}, BatchHeader{}, fmt.Errorf("cardimport session cannot be finalized: %w", core_errors.ErrConflict)
	}
	view := store.viewLocked(session)
	if !view.Ready || view.CardsCount == 0 {
		return FinalizedSession{}, BatchHeader{}, fmt.Errorf("cardimport session has no valid batch: %w", core_errors.ErrConflict)
	}
	finalized := FinalizedSession{
		Session: session.Session, User: user, Command: command, CommandDigest: digest,
		Files: make([]File, 0, len(view.Files)), Items: make([]BatchItem, 0, view.CardsCount),
	}
	groups := make(map[SourceGroupKey]struct{})
	barcodes := make(map[string]int)
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.Status == FileStatusAbandoned {
			continue
		}
		finalized.Files = append(finalized.Files, file.File)
		for _, card := range file.cards {
			position := len(finalized.Items) + 1
			for _, barcode := range card.Barcodes() {
				if barcode == "" {
					continue
				}
				if previous, exists := barcodes[barcode]; exists {
					return FinalizedSession{}, BatchHeader{}, fmt.Errorf("barcode is duplicated at positions %d and %d: %w", previous, position, core_errors.ErrConflict)
				}
				barcodes[barcode] = position
			}
			key := NewSourceGroupKey(id, card.Group, card.Variant.VendorCode)
			groups[key] = struct{}{}
			finalized.Items = append(finalized.Items, BatchItem{
				Position: position, SourceFileID: id, SourceRows: append([]int(nil), card.SourceRows...),
				SourceGroupKey: key, VendorCode: card.Variant.VendorCode, Payload: card,
			})
		}
	}
	finalized.GroupsCount = len(groups)
	checksum, err := BatchChecksum(session.Purpose, finalized.GroupsCount, finalized.Items)
	if err != nil {
		return FinalizedSession{}, BatchHeader{}, err
	}
	finalized.Checksum = checksum
	session.finalizing = true
	return finalized, BatchHeader{}, nil
}

func (store *sessionStore) finishFinalization(id SessionID, digest Digest, batch BatchHeader, err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.sessions[id]
	if session == nil {
		return
	}
	session.finalizing = false
	if err != nil {
		return
	}
	session.Status = SessionStatusFinalized
	session.Revision++
	session.UpdatedAt = time.Now().UTC()
	session.batch = batch
	session.command = digest
	delete(store.byAuthor, session.AuthorTelegramID)
	session.files = nil
	session.fileOrder = nil
	session.cross = nil
}
