package platform_runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultAdvisoryKey   int64 = 0x5742534552564943
	defaultSingletonKey        = "wb-service:v1"
	defaultProbeInterval       = 5 * time.Second
	maxLeaderIDLength          = 128
)

var ErrSingletonHeld = errors.New("wb-service singleton lock is already held")

type SessionConnection interface {
	Exec(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Release()
}

type ConnectionAcquirer interface {
	Acquire(ctx context.Context) (SessionConnection, error)
}

type PGXConnectionAcquirer struct {
	pool *pgxpool.Pool
}

func NewPGXConnectionAcquirer(pool *pgxpool.Pool) *PGXConnectionAcquirer {
	if pool == nil {
		panic("platform runtime PostgreSQL pool is nil")
	}

	return &PGXConnectionAcquirer{pool: pool}
}

func (a *PGXConnectionAcquirer) Acquire(
	ctx context.Context,
) (SessionConnection, error) {
	connection, err := a.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	return connection, nil
}

type Manager struct {
	connections  ConnectionAcquirer
	advisoryKey  int64
	singletonKey string
	probeEvery   time.Duration
	newLeaderID  func() (string, error)
}

func NewManager(connections ConnectionAcquirer) *Manager {
	if connections == nil {
		panic("platform runtime connection acquirer is nil")
	}

	return &Manager{
		connections:  connections,
		advisoryKey:  defaultAdvisoryKey,
		singletonKey: defaultSingletonKey,
		probeEvery:   defaultProbeInterval,
		newLeaderID:  randomLeaderID,
	}
}

type Guard struct {
	connection   SessionConnection
	advisoryKey  int64
	singletonKey string
	leaderID     string
	epoch        int64
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}

	mu       sync.Mutex
	lostErr  error
	close    sync.Once
	closeErr error
}

func (m *Manager) Acquire(ctx context.Context) (*Guard, error) {
	if ctx == nil {
		return nil, errors.New("acquire singleton runtime: context is nil")
	}
	if m.probeEvery <= 0 {
		return nil, errors.New("acquire singleton runtime: probe interval is not positive")
	}

	leaderID, err := m.newLeaderID()
	if err != nil {
		return nil, fmt.Errorf("create singleton leader ID: %w", err)
	}
	if strings.TrimSpace(leaderID) != leaderID ||
		leaderID == "" ||
		len(leaderID) > maxLeaderIDLength {
		return nil, errors.New("create singleton leader ID: invalid value")
	}

	connection, err := m.connections.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire singleton PostgreSQL connection: %w", err)
	}
	release := true
	defer func() {
		if release {
			connection.Release()
		}
	}()

	locked, err := tryAdvisoryLock(ctx, connection, m.advisoryKey)
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, ErrSingletonHeld
	}

	epoch, err := advanceEpoch(ctx, connection, m.singletonKey, leaderID)
	if err != nil {
		_, _ = unlockAdvisory(context.WithoutCancel(ctx), connection, m.advisoryKey)
		return nil, err
	}

	guardContext, cancel := context.WithCancel(ctx)
	guard := &Guard{
		connection:   connection,
		advisoryKey:  m.advisoryKey,
		singletonKey: m.singletonKey,
		leaderID:     leaderID,
		epoch:        epoch,
		ctx:          guardContext,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	release = false
	go guard.monitor(m.probeEvery)

	return guard, nil
}

func (g *Guard) Context() context.Context {
	return g.ctx
}

func (g *Guard) Epoch() int64 {
	return g.epoch
}

func (g *Guard) SingletonKey() string {
	return g.singletonKey
}

func (g *Guard) LeaderID() string {
	return g.leaderID
}

func (g *Guard) LostError() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.lostErr
}

func (g *Guard) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("close singleton runtime: context is nil")
	}

	g.close.Do(func() {
		g.cancel()
		monitorStopped := false
		select {
		case <-g.done:
			monitorStopped = true
		case <-ctx.Done():
			g.closeErr = fmt.Errorf(
				"wait singleton monitor: %w",
				ctx.Err(),
			)
		}
		if !monitorStopped {
			return
		}
		if lostErr := g.LostError(); lostErr != nil {
			g.closeErr = errors.Join(g.closeErr, lostErr)
		}

		unlocked, err := unlockAdvisory(ctx, g.connection, g.advisoryKey)
		if err != nil {
			g.closeErr = errors.Join(
				g.closeErr,
				fmt.Errorf("unlock singleton runtime: %w", err),
			)
		} else if !unlocked && g.LostError() == nil {
			g.closeErr = errors.Join(
				g.closeErr,
				errors.New("unlock singleton runtime: lock was not held"),
			)
		}
		g.connection.Release()
	})

	return g.closeErr
}

func (g *Guard) monitor(interval time.Duration) {
	defer close(g.done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			held, err := ownsAdvisoryLock(g.ctx, g.connection, g.advisoryKey)
			if err == nil && held {
				continue
			}
			if err == nil {
				err = errors.New("singleton advisory lock ownership was lost")
			}

			g.mu.Lock()
			g.lostErr = fmt.Errorf("probe singleton runtime: %w", err)
			g.mu.Unlock()
			g.cancel()
			return
		}
	}
}

func tryAdvisoryLock(
	ctx context.Context,
	connection SessionConnection,
	key int64,
) (bool, error) {
	var locked bool
	if err := connection.QueryRow(
		ctx,
		`SELECT pg_try_advisory_lock($1);`,
		key,
	).Scan(&locked); err != nil {
		return false, fmt.Errorf("acquire singleton advisory lock: %w", err)
	}

	return locked, nil
}

func advanceEpoch(
	ctx context.Context,
	connection SessionConnection,
	singletonKey string,
	leaderID string,
) (int64, error) {
	const query = `
		INSERT INTO wb.platform_runtime_epoch (
			singleton_key,
			epoch,
			leader_id,
			acquired_at,
			updated_at
		)
		VALUES ($1, 1, $2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT (singleton_key)
		DO UPDATE SET
			epoch = wb.platform_runtime_epoch.epoch + 1,
			leader_id = EXCLUDED.leader_id,
			acquired_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
		RETURNING epoch;
	`

	var epoch int64
	if err := connection.QueryRow(
		ctx,
		query,
		singletonKey,
		leaderID,
	).Scan(&epoch); err != nil {
		return 0, fmt.Errorf("advance singleton process epoch: %w", err)
	}
	if epoch <= 0 {
		return 0, errors.New("advance singleton process epoch: non-positive epoch")
	}

	return epoch, nil
}

func ownsAdvisoryLock(
	ctx context.Context,
	connection SessionConnection,
	key int64,
) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1
			FROM pg_locks
			WHERE locktype = 'advisory'
				AND pid = pg_backend_pid()
				AND classid = (($1::bigint >> 32) & 4294967295)::oid
				AND objid = ($1::bigint & 4294967295)::oid
				AND objsubid = 1
				AND granted
		);
	`

	var held bool
	if err := connection.QueryRow(ctx, query, key).Scan(&held); err != nil {
		return false, err
	}

	return held, nil
}

func unlockAdvisory(
	ctx context.Context,
	connection SessionConnection,
	key int64,
) (bool, error) {
	var unlocked bool
	if err := connection.QueryRow(
		ctx,
		`SELECT pg_advisory_unlock($1);`,
		key,
	).Scan(&unlocked); err != nil {
		return false, err
	}

	return unlocked, nil
}

func randomLeaderID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes[:]), nil
}
