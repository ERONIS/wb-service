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


CREATE TABLE wb.platform_runtime_epoch (
    singleton_key       VARCHAR(128) PRIMARY KEY
                        CHECK (char_length(singleton_key) > 0),

    epoch               BIGINT NOT NULL
                        CHECK (epoch > 0),

    leader_id           VARCHAR(128) NOT NULL
                        CHECK (char_length(leader_id) > 0),

    acquired_at         TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE wb.platform_outbox (
    event_id            VARCHAR(128) PRIMARY KEY
                        CHECK (char_length(event_id) > 0),

    event_type          VARCHAR(128) NOT NULL
                        CHECK (char_length(event_type) > 0),

    aggregate_id        VARCHAR(256) NOT NULL
                        CHECK (char_length(aggregate_id) > 0),

    aggregate_revision  BIGINT NOT NULL
                        CHECK (aggregate_revision >= 0),

    schema_version      INTEGER NOT NULL
                        CHECK (schema_version > 0),

    payload             JSONB NOT NULL,
    payload_digest      BYTEA NOT NULL
                        CHECK (octet_length(payload_digest) = 32),

    process_epoch       BIGINT NOT NULL
                        CHECK (process_epoch > 0),

    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN (
                            'pending',
                            'processing',
                            'delivered',
                            'dead_letter'
                        )),

    attempts            INTEGER NOT NULL DEFAULT 0
                        CHECK (attempts >= 0),

    last_error_code     VARCHAR(128)
                        CHECK (
                            last_error_code IS NULL
                            OR char_length(last_error_code) > 0
                        ),

    available_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lease_owner         VARCHAR(128),
    lease_until         TIMESTAMPTZ,
    fence               BIGINT NOT NULL DEFAULT 0
                        CHECK (fence >= 0),

    delivered_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (event_type, aggregate_id, aggregate_revision),

    CHECK (
        (
            status = 'processing'
            AND lease_owner IS NOT NULL
            AND lease_until IS NOT NULL
        )
        OR (
            status <> 'processing'
            AND lease_owner IS NULL
            AND lease_until IS NULL
        )
    ),

    CHECK (
        (status = 'delivered' AND delivered_at IS NOT NULL)
        OR (status <> 'delivered' AND delivered_at IS NULL)
    )
);

CREATE INDEX platform_outbox_claim_idx
    ON wb.platform_outbox (available_at, event_id)
    WHERE status = 'pending';


CREATE TABLE wb.platform_inbox (
    consumer            VARCHAR(128) NOT NULL
                        CHECK (char_length(consumer) > 0),

    event_id            VARCHAR(128) NOT NULL
                        CHECK (char_length(event_id) > 0),

    event_type          VARCHAR(128) NOT NULL
                        CHECK (char_length(event_type) > 0),

    aggregate_id        VARCHAR(256) NOT NULL
                        CHECK (char_length(aggregate_id) > 0),

    aggregate_revision  BIGINT NOT NULL
                        CHECK (aggregate_revision >= 0),

    schema_version      INTEGER NOT NULL
                        CHECK (schema_version > 0),

    payload_digest      BYTEA NOT NULL
                        CHECK (octet_length(payload_digest) = 32),

    applied_revision    BIGINT NOT NULL
                        CHECK (applied_revision >= 0),

    applied_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (consumer, event_id),
    UNIQUE (
        consumer,
        event_type,
        aggregate_id,
        aggregate_revision
    )
);

CREATE TABLE wb.card_import_sessions (
    id                  BIGSERIAL PRIMARY KEY,

    author_user_id      INTEGER
                        REFERENCES wb.users(id)
                        ON DELETE SET NULL,

    author_telegram_id  BIGINT NOT NULL
                        CHECK (author_telegram_id > 0),

    purpose             TEXT NOT NULL
                        CHECK (purpose IN ('transfer', 'edit')),

    status              TEXT NOT NULL DEFAULT 'collecting'
                        CHECK (status IN (
                            'collecting',
                            'finalized',
                            'cancelled'
                        )),

    revision            BIGINT NOT NULL DEFAULT 0
                        CHECK (revision >= 0),

    finalized_batch_id  BIGINT,
    finalized_from_revision BIGINT
                        CHECK (
                            finalized_from_revision IS NULL
                            OR finalized_from_revision >= 0
                        ),
    finalize_idempotency_key VARCHAR(128),
    finalize_command_digest BYTEA
                        CHECK (
                            finalize_command_digest IS NULL
                            OR octet_length(finalize_command_digest) = 32
                        ),
    finalized_at        TIMESTAMPTZ,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (
            status = 'finalized'
            AND finalized_batch_id IS NOT NULL
            AND finalized_from_revision IS NOT NULL
            AND finalize_idempotency_key IS NOT NULL
            AND char_length(finalize_idempotency_key) > 0
            AND finalize_command_digest IS NOT NULL
            AND finalized_at IS NOT NULL
        )
        OR (
            status <> 'finalized'
            AND finalized_batch_id IS NULL
            AND finalized_from_revision IS NULL
            AND finalize_idempotency_key IS NULL
            AND finalize_command_digest IS NULL
            AND finalized_at IS NULL
        )
    )
);

CREATE UNIQUE INDEX card_import_sessions_one_collecting_author_idx
    ON wb.card_import_sessions (author_telegram_id)
    WHERE status = 'collecting';

CREATE INDEX card_import_sessions_author_created_at_idx
    ON wb.card_import_sessions (author_telegram_id, created_at DESC);


CREATE TABLE wb.card_import_files (
    id                        BIGSERIAL PRIMARY KEY,

    session_id                BIGINT NOT NULL
                              REFERENCES wb.card_import_sessions(id),

    telegram_file_id          TEXT NOT NULL,
    telegram_file_unique_id   TEXT NOT NULL,
    telegram_message_id       BIGINT NOT NULL
                              CHECK (telegram_message_id > 0),

    original_filename         TEXT NOT NULL
                              CHECK (char_length(original_filename) > 0),

    mime_type                 TEXT NOT NULL DEFAULT '',

    declared_size             BIGINT NOT NULL
                              CHECK (
                                  declared_size > 0
                                  AND declared_size <= 20971520
                              ),

    stored_size               BIGINT NOT NULL DEFAULT 0
                              CHECK (
                                  stored_size >= 0
                                  AND stored_size <= 20971520
                              ),

    sha256                    BYTEA,

    status                    TEXT NOT NULL DEFAULT 'reserved'
                              CHECK (status IN (
                                  'reserved',
                                  'stored',
                                  'parsing',
                                  'valid',
                                  'invalid',
                                  'abandoned'
                              )),

    created_at                TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (session_id, telegram_file_id),
    UNIQUE (session_id, telegram_file_unique_id),
    UNIQUE (session_id, telegram_message_id),

    CHECK (
        (
            status = 'reserved'
            AND stored_size = 0
            AND sha256 IS NULL
        )
        OR (
            status IN ('stored', 'parsing', 'valid', 'invalid')
            AND stored_size > 0
            AND octet_length(sha256) = 32
        )
        OR status = 'abandoned'
    )
);

CREATE INDEX card_import_files_session_status_idx
    ON wb.card_import_files (session_id, status, id);


CREATE TABLE wb.card_import_file_blobs (
    file_id             BIGINT PRIMARY KEY
                        REFERENCES wb.card_import_files(id),

    content             BYTEA NOT NULL,

    size                BIGINT NOT NULL
                        CHECK (size > 0 AND size <= 20971520),

    sha256              BYTEA NOT NULL
                        CHECK (octet_length(sha256) = 32),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CHECK (octet_length(content) = size)
);


CREATE TABLE wb.card_import_issues (
    id                  BIGSERIAL PRIMARY KEY,

    session_id          BIGINT NOT NULL
                        REFERENCES wb.card_import_sessions(id),

    file_id             BIGINT
                        REFERENCES wb.card_import_files(id),

    severity            TEXT NOT NULL
                        CHECK (severity IN ('error', 'warning')),

    code                TEXT NOT NULL
                        CHECK (char_length(code) > 0),
    sheet_name          TEXT NOT NULL DEFAULT '',
    row_number          INTEGER
                        CHECK (row_number IS NULL OR row_number > 0),
    column_name         TEXT NOT NULL DEFAULT '',
    message             TEXT NOT NULL
                        CHECK (char_length(message) > 0),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX card_import_issues_session_file_idx
    ON wb.card_import_issues (session_id, file_id, id);


CREATE TABLE wb.card_import_items (
    id                  BIGSERIAL PRIMARY KEY,

    session_id          BIGINT NOT NULL
                        REFERENCES wb.card_import_sessions(id),

    source_file_id      BIGINT NOT NULL
                        REFERENCES wb.card_import_files(id),

    position            INTEGER NOT NULL
                        CHECK (position > 0),

    source_rows         INTEGER[] NOT NULL
                        CHECK (cardinality(source_rows) > 0),

    group_value         TEXT NOT NULL,
    category            TEXT NOT NULL,
    vendor_code         TEXT NOT NULL
                        CHECK (char_length(vendor_code) > 0),

    barcodes            TEXT[] NOT NULL DEFAULT '{}',

    payload             JSONB NOT NULL
                        CHECK (jsonb_typeof(payload) = 'object'),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (source_file_id, position),
    UNIQUE (source_file_id, vendor_code)
);

CREATE INDEX card_import_items_session_vendor_code_idx
    ON wb.card_import_items (session_id, vendor_code);


CREATE TABLE wb.card_batches (
    id                  BIGSERIAL PRIMARY KEY,

    source_session_id   BIGINT NOT NULL UNIQUE
                        REFERENCES wb.card_import_sessions(id),

    purpose             TEXT NOT NULL
                        CHECK (purpose IN ('transfer', 'edit')),

    schema_version      INTEGER NOT NULL
                        CHECK (schema_version > 0),

    normalization_version INTEGER NOT NULL
                        CHECK (normalization_version > 0),

    items_count         INTEGER NOT NULL
                        CHECK (items_count > 0),

    groups_count        INTEGER NOT NULL
                        CHECK (
                            groups_count > 0
                            AND groups_count <= items_count
                        ),

    checksum            BYTEA NOT NULL
                        CHECK (octet_length(checksum) = 32),

    author_snapshot     JSONB NOT NULL
                        CHECK (jsonb_typeof(author_snapshot) = 'object'),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (id, source_session_id)
);

ALTER TABLE wb.card_import_sessions
    ADD CONSTRAINT card_import_sessions_finalized_batch_fk
    FOREIGN KEY (finalized_batch_id, id)
    REFERENCES wb.card_batches (id, source_session_id)
    DEFERRABLE INITIALLY DEFERRED;


CREATE TABLE wb.card_batch_items (
    id                  BIGSERIAL PRIMARY KEY,

    batch_id            BIGINT NOT NULL
                        REFERENCES wb.card_batches(id),

    position            INTEGER NOT NULL
                        CHECK (position > 0),

    source_file_id      BIGINT NOT NULL
                        REFERENCES wb.card_import_files(id),

    source_rows         INTEGER[] NOT NULL
                        CHECK (cardinality(source_rows) > 0),

    source_group_key    BYTEA NOT NULL
                        CHECK (octet_length(source_group_key) = 32),

    vendor_code         TEXT NOT NULL,

    payload             JSONB NOT NULL
                        CHECK (jsonb_typeof(payload) = 'object'),

    UNIQUE (batch_id, position)
);

CREATE INDEX card_batch_items_batch_vendor_code_idx
    ON wb.card_batch_items (batch_id, vendor_code);

CREATE FUNCTION wb.reject_frozen_card_batch_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'immutable card batch rows cannot be changed'
        USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION wb.validate_card_batch_item_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM wb.card_batches AS batch
        JOIN wb.card_import_sessions AS session
            ON session.id = batch.source_session_id
        JOIN wb.card_import_files AS file
            ON file.id = NEW.source_file_id
           AND file.session_id = session.id
        WHERE batch.id = NEW.batch_id
          AND session.status = 'collecting'
    ) THEN
        RAISE EXCEPTION 'card batch item source is invalid or batch is frozen'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER card_batches_reject_mutation
BEFORE UPDATE OR DELETE ON wb.card_batches
FOR EACH ROW EXECUTE FUNCTION wb.reject_frozen_card_batch_mutation();

CREATE TRIGGER card_batch_items_validate_insert
BEFORE INSERT ON wb.card_batch_items
FOR EACH ROW EXECUTE FUNCTION wb.validate_card_batch_item_insert();

CREATE TRIGGER card_batch_items_reject_mutation
BEFORE UPDATE OR DELETE ON wb.card_batch_items
FOR EACH ROW EXECUTE FUNCTION wb.reject_frozen_card_batch_mutation();
