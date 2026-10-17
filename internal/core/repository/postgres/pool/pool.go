package core_postgres_pool

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type Pool interface {
	core_postgres_transaction.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error)
	Close()
	OperationContext(context.Context) (context.Context, context.CancelFunc)
}

type ConnectionPool struct {
	*pgxpool.Pool
	opTimeout time.Duration
}

// NewConnectionPool инициализирует пул соединений к PostgreSQL с валидацией, безопасным DSN и трассировщиком.
func NewConnectionPool(
	config Config,
	ctx context.Context,
	loggers ...*zap.Logger,
) (*ConnectionPool, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate postgres config: %w", err)
	}

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(config.User, config.Password),
		Host:   net.JoinHostPort(config.Host, config.Port),
		Path:   config.Database,
	}
	query := u.Query()
	query.Set("sslmode", config.SSLMode)
	u.RawQuery = query.Encode()

	pgxconfig, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("parse pgxconfig: %w", err)
	}

	pgxconfig.MaxConns = int32(config.MaxConns)
	pgxconfig.MinConns = int32(config.MinConns)
	pgxconfig.MaxConnLifetime = config.MaxConnLifetime
	pgxconfig.MaxConnIdleTime = config.MaxConnIdleTime
	pgxconfig.HealthCheckPeriod = config.HealthCheckPeriod

	if len(loggers) > 0 && loggers[0] != nil {
		pgxconfig.ConnConfig.Tracer = newQueryTracer(loggers[0])
	}

	pool, err := pgxpool.NewWithConfig(ctx, pgxconfig)
	if err != nil {
		return nil, fmt.Errorf("create pgxpool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pgxpool: %w", err)
	}

	return &ConnectionPool{
		Pool:      pool,
		opTimeout: config.Timeout,
	}, nil
}

func (p *ConnectionPool) OperationContext(
	parent context.Context,
) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, p.opTimeout)
}
