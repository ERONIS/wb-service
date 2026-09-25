package telegramview

import (
	"context"
	"errors"
	"fmt"

	core_postgres_pool "github.com/ERONIS/wb-service/internal/core/repository/postgres/pool"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	pool core_postgres_pool.Pool
}

func New(pool core_postgres_pool.Pool) *Store {
	if pool == nil {
		panic("Telegram view PostgreSQL pool is nil")
	}
	return &Store{pool: pool}
}

func (store *Store) Get(
	ctx context.Context,
	chatID int64,
) (int, bool, error) {
	if ctx == nil || chatID == 0 {
		return 0, false, errors.New("invalid Telegram screen lookup")
	}
	queryCtx, cancel := store.pool.OperationContext(ctx)
	defer cancel()

	var messageID int
	err := store.pool.QueryRow(queryCtx, `
		SELECT message_id
		FROM wb.telegram_active_screens
		WHERE chat_id = $1
	`, chatID).Scan(&messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get Telegram active screen: %w", err)
	}
	return messageID, true, nil
}

func (store *Store) Save(
	ctx context.Context,
	chatID int64,
	messageID int,
) error {
	if ctx == nil || chatID == 0 || messageID <= 0 {
		return errors.New("invalid Telegram screen reference")
	}
	queryCtx, cancel := store.pool.OperationContext(ctx)
	defer cancel()

	_, err := store.pool.Exec(queryCtx, `
		INSERT INTO wb.telegram_active_screens (chat_id, message_id)
		VALUES ($1, $2)
		ON CONFLICT (chat_id) DO UPDATE
		SET message_id = EXCLUDED.message_id,
		    updated_at = CURRENT_TIMESTAMP
	`, chatID, messageID)
	if err != nil {
		return fmt.Errorf("save Telegram active screen: %w", err)
	}
	return nil
}
