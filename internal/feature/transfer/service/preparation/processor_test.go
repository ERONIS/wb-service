package preparation

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	core_domain "github.com/ERONIS/wb-service/internal/core/domain"
	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	cardprepare_catalog "github.com/ERONIS/wb-service/internal/feature/cardprepare/service/catalog"
	transfer_model "github.com/ERONIS/wb-service/internal/feature/transfer/service/model"
)

// mockCatalogTransport эмулирует ответы API каталога Wildberries.
type mockCatalogTransport struct {
	unavailableCabinets map[core_domain.SellerID]bool
	mu                  sync.Mutex
}

func (m *mockCatalogTransport) CardsLimits(ctx context.Context, cab core_domain.SellerID) (contentapi.CardsLimitsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unavailableCabinets[cab] {
		return contentapi.CardsLimitsResponse{
			ResponseMeta: contentapi.ResponseMeta{Error: true},
		}, nil
	}
	return contentapi.CardsLimitsResponse{
		Data: contentapi.CardsLimits{
			FreeLimits: 100,
			PaidLimits: 0,
		},
	}, nil
}

func (m *mockCatalogTransport) Subjects(ctx context.Context, cab core_domain.SellerID, q contentapi.SubjectsQuery) (contentapi.SubjectsResponse, error) {
	return contentapi.SubjectsResponse{
		Data: []contentapi.Subject{
			{SubjectID: 101, SubjectName: "Брюки"},
		},
	}, nil
}

func (m *mockCatalogTransport) SubjectCharacteristics(ctx context.Context, cab core_domain.SellerID, subID int64, q contentapi.SubjectCharacteristicsQuery) (contentapi.SubjectCharacteristicsResponse, error) {
	return contentapi.SubjectCharacteristicsResponse{
		Data: []contentapi.SubjectCharacteristic{},
	}, nil
}

func (m *mockCatalogTransport) Brands(ctx context.Context, cab core_domain.SellerID, q contentapi.BrandsQuery) (contentapi.BrandsResponse, error) {
	return contentapi.BrandsResponse{
		Brands: []contentapi.Brand{
			{ID: 1, Name: "TestBrand"},
		},
	}, nil
}

func (m *mockCatalogTransport) Colors(ctx context.Context, cab core_domain.SellerID, q contentapi.DirectoryQuery) (contentapi.DirectoryColorsResponse, error) {
	return contentapi.DirectoryColorsResponse{}, nil
}

func (m *mockCatalogTransport) Kinds(ctx context.Context, cab core_domain.SellerID, q contentapi.DirectoryQuery) (contentapi.DirectoryKindsResponse, error) {
	return contentapi.DirectoryKindsResponse{}, nil
}

func (m *mockCatalogTransport) Countries(ctx context.Context, cab core_domain.SellerID, q contentapi.DirectoryQuery) (contentapi.DirectoryCountriesResponse, error) {
	return contentapi.DirectoryCountriesResponse{}, nil
}

func (m *mockCatalogTransport) Seasons(ctx context.Context, cab core_domain.SellerID, q contentapi.DirectoryQuery) (contentapi.DirectorySeasonsResponse, error) {
	return contentapi.DirectorySeasonsResponse{}, nil
}

func (m *mockCatalogTransport) VAT(ctx context.Context, cab core_domain.SellerID, q contentapi.DirectoryQuery) (contentapi.DirectoryVATResponse, error) {
	return contentapi.DirectoryVATResponse{}, nil
}

func (m *mockCatalogTransport) TNVED(ctx context.Context, cab core_domain.SellerID, q contentapi.DirectoryTNVEDQuery) (contentapi.DirectoryTNVEDResponse, error) {
	return contentapi.DirectoryTNVEDResponse{}, nil
}

// mockTransferSource эмулирует источник данных переноса.
type mockTransferSource struct {
	transfers []transfer_model.Transfer
	pending   map[transfer_model.TransferID][]PreparationPendingGroup
	sources   map[int64]PreparationSource
	mu        sync.Mutex
}

func (m *mockTransferSource) ListPreparing(ctx context.Context, afterID transfer_model.TransferID, limit int) ([]transfer_model.Transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]transfer_model.Transfer, 0)
	for _, t := range m.transfers {
		if t.ID > afterID {
			res = append(res, t)
			if len(res) >= limit {
				break
			}
		}
	}
	return res, nil
}

func (m *mockTransferSource) ListPendingPreparationGroups(ctx context.Context, transferID transfer_model.TransferID, limit int, after PreparationPendingGroup) ([]PreparationPendingGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := m.pending[transferID]
	if len(groups) == 0 {
		return nil, nil
	}
	ordered := make([]PreparationPendingGroup, 0)
	for _, g := range groups {
		if g.SourceGroupID > after.SourceGroupID || (g.SourceGroupID == after.SourceGroupID && g.TargetID > after.TargetID) {
			ordered = append(ordered, g)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].SourceGroupID != ordered[j].SourceGroupID {
			return ordered[i].SourceGroupID < ordered[j].SourceGroupID
		}
		return ordered[i].TargetID < ordered[j].TargetID
	})
	if len(ordered) > limit {
		return ordered[:limit], nil
	}
	return ordered, nil
}

func (m *mockTransferSource) LoadPreparationSource(ctx context.Context, transferID transfer_model.TransferID, groupTargetID int64) (PreparationSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src, ok := m.sources[groupTargetID]
	if !ok {
		return PreparationSource{}, ErrProposalNotFound
	}
	return src, nil
}

func (m *mockTransferSource) removePendingGroup(transferID transfer_model.TransferID, groupTargetID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := m.pending[transferID]
	filtered := make([]PreparationPendingGroup, 0, len(groups))
	for _, g := range groups {
		if g.GroupTargetID != groupTargetID {
			filtered = append(filtered, g)
		}
	}
	m.pending[transferID] = filtered
}

// mockResultApplier сохраняет применённые команды.
type mockResultApplier struct {
	applied []ApplyPreparationResultCommand
	onApply func(ApplyPreparationResultCommand)
	mu      sync.Mutex
}

func (m *mockResultApplier) ApplyPreparationResult(ctx context.Context, tx core_postgres_transaction.DBTX, cmd ApplyPreparationResultCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applied = append(m.applied, cmd)
	if m.onApply != nil {
		m.onApply(cmd)
	}
	return nil
}

// mockPreparationProcessorUOW реализует UnitOfWork для тестирования.
type mockPreparationProcessorUOW struct {
	transactions int
	inside       bool
}

func (m *mockPreparationProcessorUOW) WithinTransaction(ctx context.Context, fn func(context.Context, core_postgres_transaction.DBTX) error) error {
	m.transactions++
	m.inside = true
	defer func() { m.inside = false }()
	return fn(ctx, nil)
}

func TestProcessorDoesNotOpenTransactionWithoutPendingGroups(t *testing.T) {
	uow := &mockPreparationProcessorUOW{}
	source := &mockTransferSource{
		transfers: []transfer_model.Transfer{{ID: 1, Phase: transfer_model.PhaseActive, Outcome: transfer_model.OutcomeRunning}},
	}
	processor := NewProcessor(cardprepare_catalog.NewPreparer(&mockCatalogTransport{}), source, &mockResultApplier{}, uow)
	for attempt := 0; attempt < 2; attempt++ {
		if err := processor.ProcessPending(context.Background()); err != nil {
			t.Fatalf("обработка подготовки завершилась ошибкой: %v", err)
		}
	}
	if uow.transactions != 0 || uow.inside {
		t.Fatalf("пустой опрос не должен открывать транзакцию: открыто %d", uow.transactions)
	}
}

// TestProcessorSkipsInactiveTransfers проверяет, что подготовка не запускается до и после активной фазы.
func TestProcessorSkipsInactiveTransfers(t *testing.T) {
	uow := &mockPreparationProcessorUOW{}
	source := &mockTransferSource{
		transfers: []transfer_model.Transfer{
			{ID: 1, Phase: transfer_model.PhaseInitializing, Outcome: transfer_model.OutcomeRunning},
			{ID: 2, Phase: transfer_model.PhaseFinished, Outcome: transfer_model.OutcomeSucceeded},
		},
	}
	processor := NewProcessor(cardprepare_catalog.NewPreparer(&mockCatalogTransport{}), source, &mockResultApplier{}, uow)
	if err := processor.ProcessPending(context.Background()); err != nil {
		t.Fatalf("обработка подготовки завершилась ошибкой: %v", err)
	}
	if uow.transactions != 0 {
		t.Fatalf("подготовка не должна запускаться для неактивных переносов: транзакций %d", uow.transactions)
	}
}

// TestProcessor_ProcessPendingWithNotify проверяет уведомление о сохранении и диагностику.
func TestProcessor_ProcessPendingWithNotify(t *testing.T) {
	ctx := context.Background()

	transport := &mockCatalogTransport{
		unavailableCabinets: make(map[core_domain.SellerID]bool),
	}
	preparer := cardprepare_catalog.NewPreparer(transport)

	tSource := &mockTransferSource{
		transfers: []transfer_model.Transfer{
			{ID: 1, Phase: transfer_model.PhaseActive, Outcome: transfer_model.OutcomeRunning},
		},
		pending: map[transfer_model.TransferID][]PreparationPendingGroup{
			1: {
				{GroupTargetID: 10, SourceGroupID: 1, TargetID: 1},
			},
		},
		sources: map[int64]PreparationSource{
			10: {
				SellerID: "cab-1",
				Items: []PreparationSourceItem{
					{
						Position: 1,
						Card: cardpipeline.Card{
							Category: "Брюки",
							Price:    1500,
							Variant: cardpipeline.Variant{
								VendorCode: "ART-1",
								Brand:      "TestBrand",
								Title:      "Тестовые брюки",
								Dimensions: cardpipeline.Dimensions{
									Length:       10,
									Width:        10,
									Height:       10,
									WeightBrutto: 1,
								},
								Sizes: []cardpipeline.Size{
									{
										TechSize: "42",
										WBSize:   "42",
										Price:    1500,
										SKUs:     []string{"SKU-1"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	applier := &mockResultApplier{}
	applier.onApply = func(cmd ApplyPreparationResultCommand) {
		tSource.removePendingGroup(cmd.TransferID, cmd.GroupTargetID)
	}
	uow := &mockPreparationProcessorUOW{}

	proc := NewProcessor(preparer, tSource, applier, uow)

	// Первый прогон сохраняет результат группы и сообщает о продвижении.
	progress := false
	err := proc.ProcessPendingWithNotify(ctx, func() { progress = true })
	if err != nil {
		t.Fatalf("ожидалось успешное выполнение, получено: %v", err)
	}
	if !progress {
		t.Fatal("ожидался progress == true при успешной подготовке группы")
	}

	if len(applier.applied) != 1 {
		t.Fatalf("ожидалась 1 применённая команда, получено: %d", len(applier.applied))
	}
	cmd := applier.applied[0]
	if cmd.Status != PreparationResultSucceeded {
		t.Errorf("ожидался статус succeeded, получен: %s", cmd.Status)
	}
	if cmd.StartedAt.IsZero() || cmd.FinishedAt.IsZero() {
		t.Errorf("временные метки StartedAt и FinishedAt должны быть установлены")
	}
	if cmd.FinishedAt.Before(cmd.StartedAt) {
		t.Errorf("FinishedAt не может быть раньше StartedAt")
	}
	if cmd.ItemPosition != 0 || cmd.Field != "" {
		t.Errorf("у успешной подготовки ItemPosition и Field должны быть пустыми")
	}

	// Второй прогон не находит групп и не сообщает о продвижении.
	progress = false
	err = proc.ProcessPendingWithNotify(ctx, func() { progress = true })
	if err != nil {
		t.Fatalf("ожидалось успешное завершение без ошибок, получено: %v", err)
	}
	if progress {
		t.Fatal("ожидался progress == false при пустом прогоне")
	}
}

// TestProcessor_AdvancePastTransientErrors_LargeBatch проверяет обработку >1000 групп при сбое одного кабинета.
func TestProcessor_AdvancePastTransientErrors_LargeBatch(t *testing.T) {
	ctx := context.Background()

	transport := &mockCatalogTransport{
		unavailableCabinets: map[core_domain.SellerID]bool{
			"cab-failing": true, // Кабинет 1 временно недоступен
		},
	}
	preparer := cardprepare_catalog.NewPreparer(transport)

	const groupsPerCabinet = 600 // Всего 1200 групп (> 1000!)
	pendingList := make([]PreparationPendingGroup, 0, groupsPerCabinet*2)
	sourcesMap := make(map[int64]PreparationSource)

	var nextID int64 = 1
	for i := 0; i < groupsPerCabinet; i++ {
		// Группа для падающего кабинета
		failID := nextID
		nextID++
		pendingList = append(pendingList, PreparationPendingGroup{
			GroupTargetID: failID,
			SourceGroupID: int64(i + 1),
			TargetID:      1,
		})
		sourcesMap[failID] = PreparationSource{
			SellerID: "cab-failing",
			Items: []PreparationSourceItem{
				{
					Position: 1,
					Card: cardpipeline.Card{
						Category: "Брюки",
						Price:    1500,
						Variant: cardpipeline.Variant{
							VendorCode: "ART-FAIL",
							Brand:      "TestBrand",
							Title:      "Брюки",
							Dimensions: cardpipeline.Dimensions{
								Length:       10,
								Width:        10,
								Height:       10,
								WeightBrutto: 1,
							},
							Sizes: []cardpipeline.Size{
								{
									TechSize: "42",
									WBSize:   "42",
									Price:    1500,
									SKUs:     []string{"SKU-FAIL"},
								},
							},
						},
					},
				},
			},
		}

		// Группа для успешного кабинета
		okID := nextID
		nextID++
		pendingList = append(pendingList, PreparationPendingGroup{
			GroupTargetID: okID,
			SourceGroupID: int64(i + 1),
			TargetID:      2,
		})
		sourcesMap[okID] = PreparationSource{
			SellerID: "cab-ok",
			Items: []PreparationSourceItem{
				{
					Position: 1,
					Card: cardpipeline.Card{
						Category: "Брюки",
						Price:    1500,
						Variant: cardpipeline.Variant{
							VendorCode: "ART-OK",
							Brand:      "TestBrand",
							Title:      "Брюки",
							Dimensions: cardpipeline.Dimensions{
								Length:       10,
								Width:        10,
								Height:       10,
								WeightBrutto: 1,
							},
							Sizes: []cardpipeline.Size{
								{
									TechSize: "42",
									WBSize:   "42",
									Price:    1500,
									SKUs:     []string{"SKU-OK"},
								},
							},
						},
					},
				},
			},
		}
	}

	tSource := &mockTransferSource{
		transfers: []transfer_model.Transfer{
			{ID: 1, Phase: transfer_model.PhaseActive, Outcome: transfer_model.OutcomeRunning},
		},
		pending: map[transfer_model.TransferID][]PreparationPendingGroup{
			1: pendingList,
		},
		sources: sourcesMap,
	}

	applier := &mockResultApplier{}
	applier.onApply = func(cmd ApplyPreparationResultCommand) {
		tSource.removePendingGroup(cmd.TransferID, cmd.GroupTargetID)
	}
	uow := &mockPreparationProcessorUOW{}

	proc := NewProcessor(preparer, tSource, applier, uow)

	// Несколько ограниченных проходов должны обойти все группы, несмотря на ошибки первого кабинета.
	doneChan := make(chan struct{})
	var progress bool
	var err error
	go func() {
		for i := 0; i < 2*groupsPerCabinet/preparationGroupBatchSize; i++ {
			var advanced bool
			err = proc.ProcessPendingWithNotify(ctx, func() { advanced = true })
			progress = progress || advanced
			if len(applier.applied) > (i+1)*preparationGroupBatchSize {
				t.Error("превышен размер порции")
			}
		}
		close(doneChan)
	}()

	select {
	case <-doneChan:
	case <-time.After(10 * time.Second):
		t.Fatal("таймаут: процессор зациклился при обработке временных ошибок кабинета")
	}

	if !progress {
		t.Errorf("ожидался progress == true (600 успешных групп)")
	}

	// Проверяем, что все 600 групп успешного кабинета завершились успешно
	var okCount int
	for _, applied := range applier.applied {
		if applied.Status == PreparationResultSucceeded {
			okCount++
		}
	}

	if okCount != groupsPerCabinet {
		t.Fatalf("ожидалось %d успешно подготовленных групп, получено %d", groupsPerCabinet, okCount)
	}

	// 2. Теперь моделируем восстановление кабинета: сбой устранён
	transport.mu.Lock()
	transport.unavailableCabinets["cab-failing"] = false
	transport.mu.Unlock()

	// Повторный запуск: оставшиеся 600 групп cab-failing теперь успешно подготавливаются
	progress = false
	for i := 0; i < groupsPerCabinet/preparationGroupBatchSize+2; i++ {
		var advanced bool
		err = proc.ProcessPendingWithNotify(ctx, func() { advanced = true })
		if err != nil {
			t.Fatal(err)
		}
		progress = progress || advanced
	}
	if !progress {
		t.Fatal("ожидался progress == true при восстановлении работы кабинета")
	}

	var totalSucceeded int
	for _, applied := range applier.applied {
		if applied.Status == PreparationResultSucceeded {
			totalSucceeded++
		}
	}
	if totalSucceeded != groupsPerCabinet*2 {
		t.Fatalf("после восстановления ожидались все %d групп, получено %d", groupsPerCabinet*2, totalSucceeded)
	}
}

func TestProcessorSkipsFailingPageAndServesNextTransfer(t *testing.T) {
	source := &mockTransferSource{
		transfers: []transfer_model.Transfer{
			{ID: 1, Phase: transfer_model.PhaseActive, Outcome: transfer_model.OutcomeRunning},
			{ID: 2, Phase: transfer_model.PhaseActive, Outcome: transfer_model.OutcomeRunning},
		},
		pending: make(map[transfer_model.TransferID][]PreparationPendingGroup),
		sources: make(map[int64]PreparationSource),
	}
	// Первые сто групп не загружаются; следующая и второй перенос дают терминальный отказ.
	for id := int64(1); id <= preparationGroupBatchSize+1; id++ {
		source.pending[1] = append(source.pending[1], PreparationPendingGroup{
			GroupTargetID: id, SourceGroupID: id, TargetID: 1})
	}
	source.pending[2] = []PreparationPendingGroup{{GroupTargetID: 1000, SourceGroupID: 1000, TargetID: 2}}
	for _, transferID := range []transfer_model.TransferID{1, 2} {
		groups := source.pending[transferID]
		g := groups[len(groups)-1]
		sellerID := core_domain.SellerID("cab-1")
		if g.TargetID == 2 {
			sellerID = "cab-2"
		}
		source.sources[g.GroupTargetID] = PreparationSource{
			SellerID: sellerID,
			Items: []PreparationSourceItem{
				{
					Position: 1,
					Card: cardpipeline.Card{
						Category: "",
					},
				},
			},
		}
	}
	applier := &mockResultApplier{onApply: func(cmd ApplyPreparationResultCommand) {
		source.removePendingGroup(cmd.TransferID, cmd.GroupTargetID)
	}}
	proc := NewProcessor(cardprepare_catalog.NewPreparer(&mockCatalogTransport{}), source, applier, &mockPreparationProcessorUOW{})
	notifications := 0
	err := proc.ProcessPendingWithNotify(context.Background(), func() {
		notifications++
		if len(applier.applied) == 0 {
			t.Error("уведомление до сохранения")
		}
	})
	if err == nil || notifications != 1 || len(applier.applied) != 1 || applier.applied[0].TransferID != 2 {
		t.Fatalf("второй перенос не обслужен: notifications=%d applied=%v err=%v", notifications, applier.applied, err)
	}
	progress := false
	err = proc.ProcessPendingWithNotify(context.Background(), func() { progress = true })
	if err != nil || !progress || len(applier.applied) != 2 || applier.applied[1].GroupTargetID != preparationGroupBatchSize+1 {
		t.Fatalf("очередь не прошла ошибочную порцию: progress=%v err=%v applied=%v", progress, err, applier.applied)
	}
	if applier.applied[1].Status != PreparationResultRejected {
		t.Fatal("ожидался терминальный отказ")
	}
	// После конца очереди ошибочные группы снова доступны для повторной попытки.
	progress = false
	err = proc.ProcessPendingWithNotify(context.Background(), func() { progress = true })
	if err == nil || progress {
		t.Fatalf("ошибочные группы не повторены: progress=%v err=%v", progress, err)
	}
}
