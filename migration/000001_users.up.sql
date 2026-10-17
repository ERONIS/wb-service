CREATE SCHEMA wb;


CREATE TABLE wb.users (
    id              BIGSERIAL PRIMARY KEY,

    tg_id           BIGINT NOT NULL UNIQUE
                    CHECK (tg_id > 0),

    tg_nickname     VARCHAR(32),

    full_name       VARCHAR(100) NOT NULL
                    CHECK (char_length(full_name) BETWEEN 3 AND 100),

    role            TEXT NOT NULL DEFAULT 'user'
                    CHECK (role IN ('user', 'partner', 'admin')),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE wb.telegram_active_screens (
    chat_id         BIGINT PRIMARY KEY
                    CHECK (chat_id <> 0),
    message_id      INTEGER NOT NULL
                    CHECK (message_id > 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CHECK (updated_at >= created_at)
);
