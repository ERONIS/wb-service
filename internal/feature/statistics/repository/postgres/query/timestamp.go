package statistics_postgres_query

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TimePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
