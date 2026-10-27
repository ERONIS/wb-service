package execution

import (
	"context"
	"slices"
	"testing"
)

type mediaCursorRepository struct {
	MediaJournalRepository
	dispatchPages    map[int64][]ProductActionCandidate
	pendingPages     map[int64][]ProductActionCandidate
	interruptedPages map[int64][]ProductActionCandidate
	dispatchCalls    []int64
	pendingCalls     []int64
	interruptedCalls []int64
}

func (repository *mediaCursorRepository) ListDispatchableMediaActions(
	_ context.Context,
	afterActionID int64,
	_ int,
) ([]ProductActionCandidate, error) {
	repository.dispatchCalls = append(repository.dispatchCalls, afterActionID)
	return repository.dispatchPages[afterActionID], nil
}

func (repository *mediaCursorRepository) ListPendingMediaActions(
	_ context.Context,
	afterActionID int64,
	_ int,
) ([]ProductActionCandidate, error) {
	repository.pendingCalls = append(repository.pendingCalls, afterActionID)
	return repository.pendingPages[afterActionID], nil
}

func (repository *mediaCursorRepository) ListInterruptedMediaActions(
	_ context.Context,
	afterActionID int64,
	_ int,
) ([]ProductActionCandidate, error) {
	repository.interruptedCalls = append(repository.interruptedCalls, afterActionID)
	return repository.interruptedPages[afterActionID], nil
}

func TestMediaPollingAdvancesPastInFlightPage(t *testing.T) {
	candidates := func(ids ...int64) []ProductActionCandidate {
		result := make([]ProductActionCandidate, 0, len(ids))
		for _, id := range ids {
			result = append(result, ProductActionCandidate{TransferID: 1, ActionID: id})
		}
		return result
	}
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "отправка", enabled: true},
		{name: "ожидание без live", enabled: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &mediaCursorRepository{}
			pages := map[int64][]ProductActionCandidate{
				0: candidates(1, 2),
				2: candidates(3, 4),
				4: candidates(1, 2),
			}
			if test.enabled {
				repository.dispatchPages = pages
			} else {
				repository.pendingPages = pages
			}
			dispatcher := &MediaDispatcher{
				repository:  repository,
				enabled:     test.enabled,
				concurrency: 1,
				inFlight: map[mediaActionKey]struct{}{
					{transferID: 1, actionID: 99}: {},
				},
			}
			for range 3 {
				if err := dispatcher.ProcessPending(context.Background()); err != nil {
					t.Fatalf("опрос медиа: %v", err)
				}
			}
			calls := repository.dispatchCalls
			if !test.enabled {
				calls = repository.pendingCalls
			}
			if !slices.Equal(calls, []int64{0, 2, 4}) {
				t.Fatalf("курсор не обошёл занятую страницу: %v", calls)
			}
		})
	}
}

func TestMediaPollingAdvancesInterruptedPage(t *testing.T) {
	repository := &mediaCursorRepository{
		interruptedPages: map[int64][]ProductActionCandidate{
			0: {{TransferID: 1, ActionID: 1}, {TransferID: 1, ActionID: 2}},
			2: {{TransferID: 1, ActionID: 3}, {TransferID: 1, ActionID: 4}},
		},
	}
	dispatcher := &MediaDispatcher{
		repository:  repository,
		concurrency: 1,
		inFlight: map[mediaActionKey]struct{}{
			{transferID: 1, actionID: 1}: {},
			{transferID: 1, actionID: 2}: {},
			{transferID: 1, actionID: 3}: {},
			{transferID: 1, actionID: 4}: {},
		},
	}
	for range 2 {
		if err := dispatcher.ProcessPending(context.Background()); err != nil {
			t.Fatalf("восстановление медиа: %v", err)
		}
	}
	if !slices.Equal(repository.interruptedCalls, []int64{0, 2}) {
		t.Fatalf("курсор восстановления не продвинулся: %v", repository.interruptedCalls)
	}
}
