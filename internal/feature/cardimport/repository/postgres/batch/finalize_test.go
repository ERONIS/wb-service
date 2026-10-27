package cardimport_batch_repository

import (
	"testing"
	"time"

	"github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	cardimport_contract "github.com/ERONIS/wb-service/internal/feature/cardimport/service/contract"
	cardimport_model "github.com/ERONIS/wb-service/internal/feature/cardimport/service/model"
)

func TestValidateFinalizedSessionSourceFiles(t *testing.T) {
	now := time.Now().UTC()
	user := domain.CreateUser(12345, "Тест Пользователь")
	finalized := cardimport_contract.FinalizedSession{
		Session: cardimport_model.Session{
			ID: 11, AuthorTelegramID: user.TelegramID,
			Status:   cardimport_model.SessionStatusCollecting,
			Revision: 3, CreatedAt: now, UpdatedAt: now,
		},
		SourceFiles: []cardimport_contract.FinalizedSourceFile{{ID: 21, SessionID: 11}, {ID: 22, SessionID: 11}},
		Items: []cardimport_model.BatchItem{
			{
				Position: 1, SourceFileID: 21, SourceGroupNumber: 1,
				Payload: cardpipeline.Card{
					SourceRows: []int{2}, Group: "Общая группа",
					Variant: cardpipeline.Variant{VendorCode: "sku-1"},
				},
			},
			{
				Position: 2, SourceFileID: 22, SourceGroupNumber: 2,
				Payload: cardpipeline.Card{
					SourceRows: []int{3}, Group: "Общая группа",
					Variant: cardpipeline.Variant{VendorCode: "sku-2"},
				},
			},
		},
		GroupsCount: 2, User: user,
		Command: cardimport_contract.FinalizeCommand{
			SessionID: 11, ExpectedRevision: 3, IdempotencyKey: "test",
		},
	}
	if err := validateFinalizedSession(finalized); err != nil {
		t.Fatalf("корректная пачка отклонена: %v", err)
	}

	t.Run("отсутствующий файл", func(t *testing.T) {
		candidate := finalized
		candidate.SourceFiles = []cardimport_contract.FinalizedSourceFile{{ID: 21, SessionID: 11}}
		if err := validateFinalizedSession(candidate); err == nil {
			t.Fatal("карточка из отсутствующего файла принята")
		}
	})
	t.Run("повтор файла", func(t *testing.T) {
		candidate := finalized
		candidate.SourceFiles = []cardimport_contract.FinalizedSourceFile{{ID: 21, SessionID: 11}, {ID: 21, SessionID: 11}, {ID: 22, SessionID: 11}}
		if err := validateFinalizedSession(candidate); err == nil {
			t.Fatal("повтор идентификатора файла принят")
		}
	})
	t.Run("файл без карточек", func(t *testing.T) {
		candidate := finalized
		candidate.SourceFiles = []cardimport_contract.FinalizedSourceFile{{ID: 21, SessionID: 11}, {ID: 22, SessionID: 11}, {ID: 23, SessionID: 11}}
		if err := validateFinalizedSession(candidate); err == nil {
			t.Fatal("лишний исходный файл принят")
		}
	})
	t.Run("файл другой сессии", func(t *testing.T) {
		candidate := finalized
		candidate.SourceFiles = append([]cardimport_contract.FinalizedSourceFile(nil), finalized.SourceFiles...)
		candidate.SourceFiles[1].SessionID = 12
		if err := validateFinalizedSession(candidate); err == nil {
			t.Fatal("файл другой сессии принят")
		}
	})
	t.Run("общий номер для разных файлов", func(t *testing.T) {
		candidate := finalized
		candidate.Items = append([]cardimport_model.BatchItem(nil), finalized.Items...)
		candidate.Items[1].SourceGroupNumber = 1
		if err := validateFinalizedSession(candidate); err == nil {
			t.Fatal("один номер у групп разных файлов принят")
		}
	})
}
