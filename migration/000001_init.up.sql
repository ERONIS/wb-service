CREATE SCHEMA wb;


CREATE TABLE wb.users (
    id              SERIAL PRIMARY KEY,

    tg_id           VARCHAR(10) NOT NULL UNIQUE
                    CHECK (tg_id ~ '^[0-9]{9,10}$'),

    tg_nickname     VARCHAR(32),

    full_name       VARCHAR(100) NOT NULL
                    CHECK (char_length(full_name) BETWEEN 3 AND 100),

    role            TEXT NOT NULL DEFAULT 'user'
                    CHECK (role IN ('user', 'admin')),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE wb.access_requests (
    id              SERIAL PRIMARY KEY,

    tg_id           VARCHAR(10) NOT NULL UNIQUE
                    CHECK (tg_id ~ '^[0-9]{9,10}$'),

    tg_nickname     VARCHAR(32),

    full_name       VARCHAR(100) NOT NULL
                    CHECK (char_length(full_name) BETWEEN 3 AND 100),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE wb.excel_file (
    id               SERIAL PRIMARY KEY,

    author_user_id   INTEGER NOT NULL 
                     REFERENCES wb.users(id)
                     ON DELETE CASCADE,

    payload          JSONB NOT NULL, 

    created_at       TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
