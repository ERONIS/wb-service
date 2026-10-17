package sequence

import (
	"context"
	"fmt"

	core_postgres_transaction "github.com/ERONIS/wb-service/internal/core/repository/postgres/transaction"
)

func ReserveIDs(
	ctx context.Context,
	tx core_postgres_transaction.DBTX,
	sequence string,
	count int,
) ([]int64, error) {
	const query = `
		SELECT nextval($1::regclass)
		FROM generate_series(1, $2)`
	rows, err := tx.Query(ctx, query, sequence, count)
	if err != nil {
		return nil, fmt.Errorf("query PostgreSQL sequence: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0, max(count, 0))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL sequence ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL sequence IDs: %w", err)
	}
	return ids, nil
}
