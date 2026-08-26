CREATE TABLE wb.telegram_active_screens (
    chat_id         BIGINT PRIMARY KEY
                    CHECK (chat_id <> 0),
    message_id      INTEGER NOT NULL
                    CHECK (message_id > 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CHECK (updated_at >= created_at)
);
