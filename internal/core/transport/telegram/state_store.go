package core_transport_telegram

import (
	"sync"
	"time"
)

type StateClaim uint8

const (
	StateMissing StateClaim = iota
	StateClaimed
	StateBusy
	StateExpired
)

type StateLease[T any] struct {
	ID    uint64
	Value T
}

type storedState[T any] struct {
	id        uint64
	value     T
	expiresAt time.Time
	busy      bool
}

type StateStore[T any] struct {
	mu     sync.Mutex
	nextID uint64
	now    func() time.Time
	states map[int64]storedState[T]
}

func NewStateStore[T any]() *StateStore[T] {
	return &StateStore[T]{
		now:    time.Now,
		states: make(map[int64]storedState[T]),
	}
}

func (store *StateStore[T]) Begin(
	telegramID int64,
	value T,
	ttl time.Duration,
) uint64 {
	if store == nil || telegramID <= 0 || ttl <= 0 {
		panic("Telegram state registration is invalid")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextID++
	if store.nextID == 0 {
		store.nextID++
	}
	store.states[telegramID] = storedState[T]{
		id:        store.nextID,
		value:     value,
		expiresAt: store.now().Add(ttl),
	}
	return store.nextID
}

func (store *StateStore[T]) Claim(
	telegramID int64,
) (StateLease[T], StateClaim) {
	if telegramID <= 0 {
		return StateLease[T]{}, StateMissing
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[telegramID]
	if !exists {
		return StateLease[T]{}, StateMissing
	}
	lease := StateLease[T]{ID: state.id, Value: state.value}
	if state.busy {
		return lease, StateBusy
	}
	if store.now().After(state.expiresAt) {
		delete(store.states, telegramID)
		return lease, StateExpired
	}
	state.busy = true
	store.states[telegramID] = state
	return lease, StateClaimed
}

// Finish releases a claim for retry or removes a completed state. Matching the
// lease ID prevents a slow handler from changing a replacement flow.
func (store *StateStore[T]) Finish(
	telegramID int64,
	lease StateLease[T],
	retry bool,
	ttl time.Duration,
) bool {
	if telegramID <= 0 || lease.ID == 0 || (retry && ttl <= 0) {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[telegramID]
	if !exists || state.id != lease.ID || !state.busy {
		return false
	}
	if retry {
		state.value = lease.Value
		state.expiresAt = store.now().Add(ttl)
		state.busy = false
		store.states[telegramID] = state
	} else {
		delete(store.states, telegramID)
	}
	return true
}

func (store *StateStore[T]) Cancel(telegramID int64) {
	if telegramID <= 0 {
		return
	}
	store.mu.Lock()
	delete(store.states, telegramID)
	store.mu.Unlock()
}
