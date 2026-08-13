package platform_runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type queuedRow struct {
	value any
	err   error
}

func (r queuedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch value := r.value.(type) {
	case bool:
		*(dest[0].(*bool)) = value
	case int64:
		*(dest[0].(*int64)) = value
	default:
		return errors.New("unsupported queued row value")
	}
	return nil
}

type fakeSessionConnection struct {
	mu           sync.Mutex
	rows         []queuedRow
	queries      []string
	releaseCalls int
}

func (c *fakeSessionConnection) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (c *fakeSessionConnection) QueryRow(
	_ context.Context,
	query string,
	_ ...any,
) pgx.Row {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.queries = append(c.queries, query)
	if len(c.rows) == 0 {
		return queuedRow{err: errors.New("no queued row")}
	}
	row := c.rows[0]
	c.rows = c.rows[1:]
	return row
}

func (c *fakeSessionConnection) Release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releaseCalls++
}

type fakeAcquirer struct {
	connection SessionConnection
	err        error
}

func (a fakeAcquirer) Acquire(context.Context) (SessionConnection, error) {
	return a.connection, a.err
}

func testManager(connection *fakeSessionConnection) *Manager {
	manager := NewManager(fakeAcquirer{connection: connection})
	manager.probeEvery = time.Hour
	manager.newLeaderID = func() (string, error) { return "leader-1", nil }
	return manager
}

func TestAcquireAndCloseUseDedicatedConnection(t *testing.T) {
	t.Parallel()

	connection := &fakeSessionConnection{rows: []queuedRow{
		{value: true},     // acquire lock
		{value: int64(7)}, // advance epoch
		{value: true},     // unlock
	}}
	guard, err := testManager(connection).Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if guard.Epoch() != 7 {
		t.Fatalf("epoch = %d, want 7", guard.Epoch())
	}
	if guard.LeaderID() != "leader-1" {
		t.Fatalf("leader ID = %q, want leader-1", guard.LeaderID())
	}

	if err := guard.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if connection.releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", connection.releaseCalls)
	}
	if got := connection.queries[len(connection.queries)-1]; !strings.Contains(got, "pg_advisory_unlock") {
		t.Fatalf("last query = %q, want advisory unlock", got)
	}
}

func TestAcquireFailsWhenSingletonIsHeld(t *testing.T) {
	t.Parallel()

	connection := &fakeSessionConnection{rows: []queuedRow{{value: false}}}
	_, err := testManager(connection).Acquire(context.Background())
	if !errors.Is(err, ErrSingletonHeld) {
		t.Fatalf("Acquire() error = %v, want ErrSingletonHeld", err)
	}
	if connection.releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", connection.releaseCalls)
	}
}

func TestProbeLossCancelsGuardContext(t *testing.T) {
	t.Parallel()

	connection := &fakeSessionConnection{rows: []queuedRow{
		{value: true},
		{value: int64(1)},
		{value: false},
		{value: false},
	}}
	manager := testManager(connection)
	manager.probeEvery = time.Millisecond
	guard, err := manager.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	select {
	case <-guard.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("guard context was not cancelled after lock loss")
	}
	if guard.LostError() == nil {
		t.Fatal("LostError() = nil, want lock-loss error")
	}
	if err := guard.Close(context.Background()); err == nil {
		t.Fatal("Close() error = nil, want missing-lock error")
	}
	if connection.releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", connection.releaseCalls)
	}
}
