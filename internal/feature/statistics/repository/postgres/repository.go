package statistics_postgres_repository

import (
	"context"
	"fmt"
	"strings"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
)

type Repository struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Repository {
	if pool == nil {
		panic("statistics PostgreSQL pool is nil")
	}
	return &Repository{pool: pool}
}

func (repository *Repository) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, repository.pool.OpTimeout())
}

type whereBuilder struct {
	conditions []string
	arguments  []any
}

type rowScanner interface {
	Scan(...any) error
}

func (builder *whereBuilder) add(expression string, value any) {
	builder.arguments = append(builder.arguments, value)
	builder.conditions = append(
		builder.conditions,
		fmt.Sprintf(expression, len(builder.arguments)),
	)
}

func (builder *whereBuilder) addRaw(expression string) {
	builder.conditions = append(builder.conditions, expression)
}

func (builder *whereBuilder) clause() string {
	if len(builder.conditions) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(builder.conditions, " AND ")
}
