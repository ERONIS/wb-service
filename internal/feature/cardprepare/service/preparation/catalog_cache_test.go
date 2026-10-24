package preparation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
)

type countingCatalogTransport struct {
	CatalogTransport
	mu                  sync.Mutex
	subjectCalls        int
	characteristicCalls int
	limitsCalls         int
	subjectError        error
}

func (transport *countingCatalogTransport) CardsLimits(_ context.Context, _ core_domain.SellerID) (contentapi.CardsLimitsResponse, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.limitsCalls++
	return contentapi.CardsLimitsResponse{}, nil
}

func (transport *countingCatalogTransport) Subjects(_ context.Context, _ core_domain.SellerID, query contentapi.SubjectsQuery) (contentapi.SubjectsResponse, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.subjectCalls++
	if transport.subjectError != nil {
		return contentapi.SubjectsResponse{}, transport.subjectError
	}
	return contentapi.SubjectsResponse{
		Data: []contentapi.Subject{{SubjectID: 101, SubjectName: query.Name}},
	}, nil
}

func (transport *countingCatalogTransport) SubjectCharacteristics(_ context.Context, _ core_domain.SellerID, subjectID int64, _ contentapi.SubjectCharacteristicsQuery) (contentapi.SubjectCharacteristicsResponse, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.characteristicCalls++
	return contentapi.SubjectCharacteristicsResponse{
		Data: []contentapi.SubjectCharacteristic{{SubjectID: subjectID, CharacteristicID: 501, Name: "Цвет"}},
	}, nil
}

func TestSharedCatalogReusesOnlySchemaResponses(t *testing.T) {
	transport := &countingCatalogTransport{}
	preparer := NewPreparer(transport)
	ctx := context.Background()
	subjectQuery := contentapi.SubjectsQuery{Locale: contentapi.LocaleRU, Name: "Брюки", Limit: 100}
	characteristicQuery := contentapi.SubjectCharacteristicsQuery{Locale: contentapi.LocaleRU}

	for range 2 {
		if _, err := preparer.transport.Subjects(ctx, "seller-1", subjectQuery); err != nil {
			t.Fatal(err)
		}
		if _, err := preparer.transport.SubjectCharacteristics(ctx, "seller-1", 101, characteristicQuery); err != nil {
			t.Fatal(err)
		}
		if _, err := preparer.transport.CardsLimits(ctx, "seller-1"); err != nil {
			t.Fatal(err)
		}
	}
	if transport.subjectCalls != 1 || transport.characteristicCalls != 1 || transport.limitsCalls != 2 {
		t.Fatalf("неверное количество запросов: Subjects=%d, SubjectCharacteristics=%d, CardsLimits=%d", transport.subjectCalls, transport.characteristicCalls, transport.limitsCalls)
	}

	if _, err := preparer.transport.Subjects(ctx, "seller-2", subjectQuery); err != nil {
		t.Fatal(err)
	}
	subjectQuery.Name = "Рубашки"
	if _, err := preparer.transport.Subjects(ctx, "seller-1", subjectQuery); err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.transport.SubjectCharacteristics(ctx, "seller-1", 102, characteristicQuery); err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.transport.SubjectCharacteristics(ctx, "seller-2", 101, characteristicQuery); err != nil {
		t.Fatal(err)
	}
	if transport.subjectCalls != 3 || transport.characteristicCalls != 3 {
		t.Fatalf("ключи кабинета и запроса смешались: Subjects=%d, SubjectCharacteristics=%d", transport.subjectCalls, transport.characteristicCalls)
	}
}

func TestSharedCatalogExpiresAndDoesNotCacheErrors(t *testing.T) {
	transport := &countingCatalogTransport{subjectError: errors.New("временная ошибка")}
	preparer := NewPreparer(transport)
	shared := preparer.transport.(*sharedCatalog)
	now := time.Now()
	shared.now = func() time.Time { return now }
	ctx := context.Background()
	query := contentapi.SubjectsQuery{Locale: contentapi.LocaleRU, Name: "Брюки", Limit: 100}

	if _, err := preparer.transport.Subjects(ctx, "seller-1", query); err == nil {
		t.Fatal("ожидалась ошибка каталога")
	}
	transport.subjectError = nil
	if _, err := preparer.transport.Subjects(ctx, "seller-1", query); err != nil {
		t.Fatal(err)
	}
	if transport.subjectCalls != 2 {
		t.Fatalf("ошибка попала в кеш: запросов %d", transport.subjectCalls)
	}

	now = now.Add(schemaCacheTTL)
	if _, err := preparer.transport.Subjects(ctx, "seller-1", query); err != nil {
		t.Fatal(err)
	}
	if transport.subjectCalls != 3 {
		t.Fatalf("после истечения срока ожидался новый запрос, получено %d", transport.subjectCalls)
	}
}
