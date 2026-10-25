package cardimport_staging

import (
	"context"
	"fmt"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_errors "github.com/ERONIS/wb-service/internal/core/errors"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

// PrepareFinalization проверяет сессию и собирает карточки для сохранения.
func (store *Store) PrepareFinalization(ctx context.Context, user domain.User, command cardimport_contract.FinalizeCommand) (cardimport_contract.FinalizedSession, cardimport_model.BatchHeader, error) {
	if err := ctx.Err(); err != nil {
		return cardimport_contract.FinalizedSession{}, cardimport_model.BatchHeader{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, err := store.sessionLocked(user.TelegramID, command.SessionID)
	if err != nil {
		return cardimport_contract.FinalizedSession{}, cardimport_model.BatchHeader{}, err
	}
	if session.Status == cardimport_model.SessionStatusFinalized {
		return cardimport_contract.FinalizedSession{}, session.batch, nil
	}
	if session.Status != cardimport_model.SessionStatusCollecting || session.finalizing || session.Revision != command.ExpectedRevision {
		return cardimport_contract.FinalizedSession{}, cardimport_model.BatchHeader{}, fmt.Errorf("cardimport session cannot be finalized: %w", core_errors.ErrConflict)
	}
	view := store.viewLocked(session)
	if !view.Ready {
		return cardimport_contract.FinalizedSession{}, cardimport_model.BatchHeader{}, fmt.Errorf("cardimport session has no valid batch: %w", core_errors.ErrConflict)
	}
	selected := session.selectedCards()
	finalized := cardimport_contract.FinalizedSession{
		Session: session.Session, User: user, Command: command,
		Files: make([]cardimport_model.File, 0, len(view.Files)), Items: make([]cardimport_model.BatchItem, 0, len(selected)),
	}
	groups := make(map[cardpipeline.SourceGroupKey]struct{})
	seenFiles := make(map[cardimport_model.FileID]struct{})
	for _, item := range selected {
		if _, exists := seenFiles[item.fileID]; !exists {
			finalized.Files = append(finalized.Files, session.files[item.fileID].File)
			seenFiles[item.fileID] = struct{}{}
		}
		position := len(finalized.Items) + 1
		key := cardimport_model.NewSourceGroupKey(item.fileID, item.card.Group)
		groups[key] = struct{}{}
		finalized.Items = append(finalized.Items, cardimport_model.BatchItem{
			Position: position, SourceFileID: item.fileID, SourceRows: append([]int(nil), item.card.SourceRows...),
			SourceGroupKey: key, VendorCode: item.card.Variant.VendorCode, Payload: item.card,
		})
	}
	finalized.GroupsCount = len(groups)
	session.finalizing = true
	return finalized, cardimport_model.BatchHeader{}, nil
}

// FinishFinalization снимает блокировку и завершает успешную финализацию.
func (store *Store) FinishFinalization(id cardimport_model.SessionID, batch cardimport_model.BatchHeader, err error) {
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
	session.Status = cardimport_model.SessionStatusFinalized
	session.Revision++
	session.UpdatedAt = time.Now().UTC()
	session.batch = batch
	delete(store.byAuthor, session.AuthorTelegramID)
	session.files = nil
	session.fileOrder = nil
	session.cross = nil
}
