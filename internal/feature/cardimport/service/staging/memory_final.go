package cardimport_staging

import (
	"context"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_batch "github.com/ERONIS/wb-service/internal/feature/cardimport/service/batch"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_intake "github.com/ERONIS/wb-service/internal/feature/cardimport/service/intake"
)

// PrepareFinalization проверяет сессию и собирает карточки для сохранения.
func (store *Store) PrepareFinalization(ctx context.Context, user domain.User, command cardimport_contract.FinalizeCommand) (cardimport_contract.FinalizedSession, cardimport_batch.BatchHeader, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_contract.FinalizedSession{}, cardimport_batch.BatchHeader{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(user.TelegramID, command.SessionID)
	if err != nil {
		return cardimport_contract.FinalizedSession{}, cardimport_batch.BatchHeader{}, err
	}
	if session.Status == cardimport_intake.SessionStatusFinalized {
		return cardimport_contract.FinalizedSession{}, session.batch, nil
	}
	if session.Status != cardimport_intake.SessionStatusCollecting || session.finalizing || session.Revision != command.ExpectedRevision {
		return cardimport_contract.FinalizedSession{}, cardimport_batch.BatchHeader{}, fmt.Errorf("cardimport session cannot be finalized: %w", core_errors.ErrConflict)
	}
	view := store.viewLocked(session)
	if !view.Ready || view.CardsCount == 0 {
		return cardimport_contract.FinalizedSession{}, cardimport_batch.BatchHeader{}, fmt.Errorf("cardimport session has no valid batch: %w", core_errors.ErrConflict)
	}
	finalized := cardimport_contract.FinalizedSession{
		Session: session.Session, User: user, Command: command,
		Files: make([]cardimport_intake.File, 0, len(view.Files)), Items: make([]cardimport_batch.BatchItem, 0, view.CardsCount),
	}
	groups := make(map[cardpipeline.SourceGroupKey]struct{})
	barcodes := make(map[string]int)
	for _, id := range session.fileOrder {
		file := session.files[id]
		if file.Status == cardimport_intake.FileStatusAbandoned {
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
					return cardimport_contract.FinalizedSession{}, cardimport_batch.BatchHeader{}, fmt.Errorf("barcode is duplicated at positions %d and %d: %w", previous, position, core_errors.ErrConflict)
				}
				barcodes[barcode] = position
			}
			key := cardimport_batch.NewSourceGroupKey(id, card.Group)
			groups[key] = struct{}{}
			finalized.Items = append(finalized.Items, cardimport_batch.BatchItem{
				Position: position, SourceFileID: id, SourceRows: append([]int(nil), card.SourceRows...),
				SourceGroupKey: key, VendorCode: card.Variant.VendorCode, Payload: card,
			})
		}
	}
	finalized.GroupsCount = len(groups)
	session.finalizing = true
	return finalized, cardimport_batch.BatchHeader{}, nil
}

// FinishFinalization снимает блокировку и завершает успешную финализацию.
func (store *Store) FinishFinalization(id cardimport_intake.SessionID, batch cardimport_batch.BatchHeader, err error) {
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
	session.Status = cardimport_intake.SessionStatusFinalized
	session.Revision++
	session.UpdatedAt = time.Now().UTC()
	session.batch = batch
	delete(store.byAuthor, session.AuthorTelegramID)
	session.files = nil
	session.fileOrder = nil
	session.cross = nil
}
