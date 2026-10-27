package wb

import (
	"context"
	"errors"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	"go.uber.org/zap"
)

func TestCandidateGenerationAssignedOnActivation(t *testing.T) {
	clientset, err := NewClientset(context.Background(), zap.NewNop(), contentapi.BucketCatalog())
	if err != nil {
		t.Fatal(err)
	}
	defer clientset.CloseIdleConnections()

	sellerID := domain.SellerID("a723f78d-4d8f-45eb-a40b-3d46841d1688")
	first, err := clientset.NewCandidate(sellerID, "Кабинет", "header.payload.first")
	if err != nil {
		t.Fatal(err)
	}
	if !first.generation.IsZero() {
		t.Fatal("поколение назначено до сохранения кабинета")
	}
	if err := clientset.ActivateCandidate(first, 0); err == nil {
		t.Fatal("нулевое поколение принято")
	}
	if err := clientset.ActivateCandidate(first, -1); err == nil {
		t.Fatal("отрицательное поколение принято")
	}
	if err := clientset.ActivateCandidate(first, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := clientset.PinnedExecutor(sellerID, 2); err != nil {
		t.Fatal(err)
	}

	stale, err := clientset.NewCandidate(sellerID, "Кабинет", "header.payload.stale")
	if err != nil {
		t.Fatal(err)
	}
	if err := clientset.ActivateCandidate(stale, 1); !errors.Is(err, ErrClientGenerationChanged) {
		t.Fatalf("устаревшее поколение не отклонено: %v", err)
	}
	if executor, err := clientset.PinnedExecutor(sellerID, 2); err != nil || executor != first.Executor() {
		t.Fatalf("устаревший клиент заменил активный: %v", err)
	}

	newer, err := clientset.NewCandidate(sellerID, "Кабинет", "header.payload.newer")
	if err != nil {
		t.Fatal(err)
	}
	if err := clientset.ActivateCandidate(newer, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := clientset.PinnedExecutor(sellerID, 2); !errors.Is(err, ErrClientGenerationChanged) {
		t.Fatalf("старое закреплённое поколение не отклонено: %v", err)
	}
	if executor, err := clientset.PinnedExecutor(sellerID, 3); err != nil || executor != newer.Executor() {
		t.Fatalf("новое поколение не активировано: %v", err)
	}
}
