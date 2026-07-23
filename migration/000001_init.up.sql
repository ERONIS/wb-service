CREATE SCHEMA wb;


CREATE TABLE wb.users (
    id              SERIAL PRIMARY KEY,

    tg_id           BIGINT NOT NULL UNIQUE
                    CHECK (tg_id > 0),

    tg_nickname     VARCHAR(32),

    full_name       VARCHAR(100) NOT NULL
                    CHECK (char_length(full_name) BETWEEN 3 AND 100),

    role            TEXT NOT NULL DEFAULT 'user'
                    CHECK (role IN ('user', 'admin')),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE wb.card_imports (
    id                BIGSERIAL PRIMARY KEY,

    author_user_id    INTEGER NOT NULL
                      REFERENCES wb.users(id)
                      ON DELETE CASCADE,

    purpose           TEXT NOT NULL
                      CHECK (purpose IN ('transfer', 'edit')),

    original_filename TEXT NOT NULL,
    telegram_file_id  TEXT NOT NULL,
    mime_type         TEXT NOT NULL,

    payload           JSONB NOT NULL
                      CHECK (jsonb_typeof(payload) = 'array'),

    cards_count       INTEGER NOT NULL
                      CHECK (cards_count >= 0),

    status            TEXT NOT NULL DEFAULT 'received'
                      CHECK (status IN ('received', 'ready', 'error')),

    error_message     TEXT NOT NULL DEFAULT '',

    created_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX card_imports_author_created_at_idx
    ON wb.card_imports (author_user_id, created_at DESC);

CREATE INDEX card_imports_purpose_status_idx
    ON wb.card_imports (purpose, status);
