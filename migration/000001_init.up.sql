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


CREATE TABLE wb.mutation_target_bindings (
    cabinet_id              VARCHAR(128) PRIMARY KEY
                            CHECK (char_length(cabinet_id) > 0),
    seller_key              BYTEA NOT NULL UNIQUE
                            CHECK (octet_length(seller_key) = 32),
    binding_revision        BIGINT NOT NULL DEFAULT 1
                            CHECK (binding_revision > 0),
    capability_revision     BIGINT NOT NULL DEFAULT 1
                            CHECK (capability_revision > 0),
    content_read            BOOLEAN NOT NULL,
    content_write           BOOLEAN NOT NULL,
    client_generation       BYTEA NOT NULL
                            CHECK (octet_length(client_generation) = 32),
    credential_expires_at   TIMESTAMPTZ NOT NULL,
    verified_at             TIMESTAMPTZ NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN (
                                'active',
                                'identity_mismatch'
                            )),
    mismatch_seller_key     BYTEA
                            CHECK (
                                mismatch_seller_key IS NULL
                                OR octet_length(mismatch_seller_key) = 32
                            ),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (status = 'active' AND mismatch_seller_key IS NULL)
        OR (
            status = 'identity_mismatch'
            AND mismatch_seller_key IS NOT NULL
        )
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

    finalized_at        TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (id, source_session_id)
);

CREATE INDEX card_batches_finalized_cursor_idx
    ON wb.card_batches (finalized_at, id);

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


CREATE TABLE wb.transfers (
    id                      BIGSERIAL PRIMARY KEY,

    batch_id                BIGINT NOT NULL UNIQUE
                            REFERENCES wb.card_batches(id),

    phase                   TEXT NOT NULL DEFAULT 'initializing'
                            CHECK (phase IN (
                                'initializing',
                                'preparing',
                                'awaiting_authorization',
                                'publishing',
                                'reconciling',
                                'media',
                                'finished'
                            )),
    outcome                 TEXT NOT NULL DEFAULT 'running'
                            CHECK (outcome IN (
                                'running',
                                'succeeded',
                                'partial',
                                'rejected',
                                'unresolved',
                                'failed',
                                'cancelled'
                            )),
    attention_code          VARCHAR(128)
                            CHECK (
                                attention_code IS NULL
                                OR char_length(attention_code) > 0
                            ),

    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),

    batch_schema_version    INTEGER NOT NULL
                            CHECK (batch_schema_version > 0),
    batch_normalization_version INTEGER NOT NULL
                            CHECK (batch_normalization_version > 0),
    batch_checksum          BYTEA NOT NULL
                            CHECK (octet_length(batch_checksum) = 32),
    items_count             INTEGER NOT NULL
                            CHECK (items_count > 0),
    groups_count            INTEGER NOT NULL
                            CHECK (
                                groups_count > 0
                                AND groups_count <= items_count
                            ),

    cohort_name             VARCHAR(128) NOT NULL
                            CHECK (char_length(cohort_name) > 0),
    target_snapshot_revision BYTEA NOT NULL
                            CHECK (octet_length(target_snapshot_revision) = 32),
    target_set_root         BYTEA NOT NULL
                            CHECK (octet_length(target_set_root) = 32),
    targets_count           INTEGER NOT NULL
                            CHECK (targets_count > 0),
    item_targets_count      BIGINT NOT NULL
                            CHECK (item_targets_count > 0),
    group_targets_count     BIGINT NOT NULL
                            CHECK (group_targets_count > 0),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (
            phase = 'finished'
            AND outcome <> 'running'
            AND finished_at IS NOT NULL
        )
        OR (
            phase <> 'finished'
            AND outcome = 'running'
            AND finished_at IS NULL
        )
    ),
    CHECK (
        (outcome = 'running' AND attention_code IS NULL)
        OR (
            outcome IN ('failed', 'unresolved')
            AND attention_code IS NOT NULL
        )
        OR outcome IN (
            'succeeded', 'partial', 'rejected', 'cancelled'
        )
    ),
    CHECK (started_at >= created_at),
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (updated_at >= created_at)
);

CREATE INDEX transfers_phase_outcome_id_idx
ON wb.transfers (phase, outcome, id);


CREATE TABLE wb.transfer_targets (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL
                            REFERENCES wb.transfers(id),

    position                INTEGER NOT NULL
                            CHECK (position > 0),
    cabinet_id              VARCHAR(128) NOT NULL
                            CHECK (char_length(cabinet_id) > 0),
    seller_key              BYTEA NOT NULL
                            CHECK (octet_length(seller_key) = 32),
    binding_revision        BIGINT NOT NULL
                            CHECK (binding_revision > 0),
    capability_revision     BIGINT NOT NULL
                            CHECK (capability_revision > 0),
    content_read            BOOLEAN NOT NULL,
    content_write           BOOLEAN NOT NULL,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, position),
    UNIQUE (transfer_id, cabinet_id),
    UNIQUE (transfer_id, seller_key),

    CHECK (content_read AND content_write)
);


CREATE TABLE wb.transfer_groups (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL
                            REFERENCES wb.transfers(id),
    source_group_key        BYTEA NOT NULL
                            CHECK (octet_length(source_group_key) = 32),
    source_file_id          BIGINT NOT NULL
                            CHECK (source_file_id > 0),
    source_group_name       TEXT NOT NULL,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, source_group_key)
);


CREATE TABLE wb.transfer_items (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL
                            REFERENCES wb.transfers(id),
    batch_item_id           BIGINT NOT NULL
                            CHECK (batch_item_id > 0),
    position                INTEGER NOT NULL
                            CHECK (position > 0),
    source_file_id          BIGINT NOT NULL
                            CHECK (source_file_id > 0),
    source_rows             INTEGER[] NOT NULL
                            CHECK (cardinality(source_rows) > 0),
    source_group_id         BIGINT NOT NULL,
    vendor_code             TEXT NOT NULL
                            CHECK (char_length(vendor_code) > 0),
    payload                 JSONB NOT NULL
                            CHECK (jsonb_typeof(payload) = 'object'),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, source_group_id, id),
    UNIQUE (transfer_id, batch_item_id),
    UNIQUE (transfer_id, position),

    FOREIGN KEY (transfer_id, source_group_id)
        REFERENCES wb.transfer_groups (transfer_id, id)
);


CREATE TABLE wb.transfer_group_members (
    transfer_id             BIGINT NOT NULL,
    source_group_id         BIGINT NOT NULL,
    transfer_item_id        BIGINT NOT NULL,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (transfer_id, source_group_id, transfer_item_id),

    FOREIGN KEY (transfer_id, source_group_id)
        REFERENCES wb.transfer_groups (transfer_id, id),
    FOREIGN KEY (transfer_id, source_group_id, transfer_item_id)
        REFERENCES wb.transfer_items (transfer_id, source_group_id, id)
);


CREATE TABLE wb.transfer_group_targets (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    source_group_id         BIGINT NOT NULL,
    target_id               BIGINT NOT NULL,
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),
    preparation_status      TEXT NOT NULL DEFAULT 'not_started'
                            CHECK (preparation_status IN (
                                'not_started', 'running', 'succeeded',
                                'rejected', 'unresolved', 'skipped'
                            )),
    preparation_result_revision BIGINT NOT NULL DEFAULT 0
                            CHECK (preparation_result_revision IN (0, 1)),
    preparation_id          BIGINT
                            CHECK (preparation_id IS NULL OR preparation_id > 0),
    preparation_group_id    BIGINT
                            CHECK (
                                preparation_group_id IS NULL
                                OR preparation_group_id > 0
                            ),
    preparation_result_code VARCHAR(128)
                            CHECK (
                                preparation_result_code IS NULL
                                OR char_length(preparation_result_code) > 0
                            ),
    preparation_proposal_root BYTEA
                            CHECK (
                                preparation_proposal_root IS NULL
                                OR octet_length(preparation_proposal_root) = 32
                            ),
    publication_status      TEXT NOT NULL DEFAULT 'not_started'
                            CHECK (publication_status IN (
                                'not_started', 'running', 'succeeded',
                                'rejected', 'unresolved', 'skipped'
                            )),
    media_status            TEXT NOT NULL DEFAULT 'not_started'
                            CHECK (media_status IN (
                                'not_started', 'running', 'succeeded',
                                'rejected', 'unresolved', 'skipped'
                            )),
    overall_outcome         TEXT NOT NULL DEFAULT 'running'
                            CHECK (overall_outcome IN (
                                'running', 'success', 'skipped', 'rejected',
                                'partial', 'unresolved', 'internal_error'
                            )),
    attention_code          VARCHAR(128)
                            CHECK (
                                attention_code IS NULL
                                OR char_length(attention_code) > 0
                            ),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at              TIMESTAMPTZ,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, source_group_id, id),
    UNIQUE (transfer_id, source_group_id, target_id),

    FOREIGN KEY (transfer_id, source_group_id)
        REFERENCES wb.transfer_groups (transfer_id, id),
    FOREIGN KEY (transfer_id, target_id)
        REFERENCES wb.transfer_targets (transfer_id, id),

    CHECK (
        (
            preparation_status IN ('not_started', 'running')
            AND preparation_result_revision = 0
            AND preparation_id IS NULL
            AND preparation_group_id IS NULL
            AND preparation_result_code IS NULL
            AND preparation_proposal_root IS NULL
        )
        OR (
            preparation_status = 'succeeded'
            AND preparation_result_revision = 1
            AND preparation_id IS NOT NULL
            AND preparation_group_id IS NOT NULL
            AND preparation_result_code = 'prepared'
            AND preparation_proposal_root IS NOT NULL
        )
        OR (
            preparation_status IN ('rejected', 'unresolved')
            AND preparation_result_revision = 1
            AND preparation_id IS NOT NULL
            AND preparation_group_id IS NOT NULL
            AND preparation_result_code IS NOT NULL
            AND preparation_proposal_root IS NULL
        )
        OR (
            preparation_status = 'skipped'
            AND preparation_result_revision = 0
            AND preparation_id IS NULL
            AND preparation_group_id IS NULL
            AND preparation_result_code IS NULL
            AND preparation_proposal_root IS NULL
        )
    ),
    CHECK (
        (
            overall_outcome = 'running'
            AND attention_code IS NULL
            AND finished_at IS NULL
        )
        OR (
            overall_outcome IN ('unresolved', 'internal_error')
            AND attention_code IS NOT NULL
            AND finished_at IS NOT NULL
        )
        OR (
            overall_outcome IN (
                'success', 'skipped', 'rejected', 'partial'
            )
            AND finished_at IS NOT NULL
        )
    ),
    CHECK (started_at IS NULL OR started_at >= created_at),
    CHECK (finished_at IS NULL OR (
        started_at IS NOT NULL AND finished_at >= started_at
    )),
    CHECK (updated_at >= created_at)
);


CREATE TABLE wb.transfer_item_targets (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    source_group_id         BIGINT NOT NULL,
    transfer_item_id        BIGINT NOT NULL,
    group_target_id         BIGINT NOT NULL,
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),
    state                   TEXT NOT NULL DEFAULT 'pending'
                            CHECK (state IN (
                                'pending', 'running', 'terminal'
                            )),
    outcome_class           TEXT
                            CHECK (
                                outcome_class IS NULL
                                OR outcome_class IN (
                                    'success', 'skipped', 'rejected', 'partial',
                                    'unresolved', 'internal_error'
                                )
                            ),
    outcome_code            VARCHAR(128)
                            CHECK (
                                outcome_code IS NULL
                                OR char_length(outcome_code) > 0
                            ),
    nm_id                   BIGINT
                            CHECK (nm_id IS NULL OR nm_id > 0),
    source_action_id        BIGINT
                            CHECK (
                                source_action_id IS NULL
                                OR source_action_id > 0
                            ),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at              TIMESTAMPTZ,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, group_target_id, id),
    UNIQUE (transfer_id, transfer_item_id, group_target_id),

    FOREIGN KEY (transfer_id, source_group_id, transfer_item_id)
        REFERENCES wb.transfer_group_members (
            transfer_id,
            source_group_id,
            transfer_item_id
        ),
    FOREIGN KEY (transfer_id, source_group_id, group_target_id)
        REFERENCES wb.transfer_group_targets (
            transfer_id,
            source_group_id,
            id
        ),

    CHECK (
        (
            state = 'pending'
            AND outcome_class IS NULL
            AND outcome_code IS NULL
            AND nm_id IS NULL
            AND source_action_id IS NULL
            AND started_at IS NULL
            AND finished_at IS NULL
        )
        OR (
            state = 'running'
            AND outcome_class IS NULL
            AND outcome_code IS NULL
            AND finished_at IS NULL
            AND started_at IS NOT NULL
        )
        OR (
            state = 'terminal'
            AND outcome_class IS NOT NULL
            AND outcome_code IS NOT NULL
            AND started_at IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CHECK (started_at IS NULL OR started_at >= created_at),
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (updated_at >= created_at)
);


CREATE TABLE wb.card_preparations (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL UNIQUE
                            REFERENCES wb.transfers(id),
    status                  TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN (
                                'pending', 'processing', 'completed',
                                'completed_with_issues', 'failed'
                            )),
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),
    expected_group_targets  BIGINT NOT NULL
                            CHECK (expected_group_targets > 0),
    batch_schema_version    INTEGER NOT NULL
                            CHECK (batch_schema_version > 0),
    batch_normalization_version INTEGER NOT NULL
                            CHECK (batch_normalization_version > 0),
    completed_group_targets BIGINT NOT NULL DEFAULT 0
                            CHECK (completed_group_targets >= 0),
    rejected_group_targets  BIGINT NOT NULL DEFAULT 0
                            CHECK (rejected_group_targets >= 0),
    unresolved_group_targets BIGINT NOT NULL DEFAULT 0
                            CHECK (unresolved_group_targets >= 0),
    batch_checksum          BYTEA NOT NULL
                            CHECK (octet_length(batch_checksum) = 32),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at              TIMESTAMPTZ,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),

    CHECK (
        completed_group_targets
        + rejected_group_targets
        + unresolved_group_targets
        <= expected_group_targets
    ),
    CHECK (
        (status = 'pending' AND started_at IS NULL AND finished_at IS NULL)
        OR (
            status = 'processing'
            AND started_at IS NOT NULL
            AND finished_at IS NULL
        )
        OR (
            status IN ('completed', 'completed_with_issues', 'failed')
            AND started_at IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CHECK (started_at IS NULL OR started_at >= created_at),
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (updated_at >= created_at)
);


CREATE TABLE wb.card_preparation_groups (
    id                      BIGSERIAL PRIMARY KEY,

    preparation_id          BIGINT NOT NULL,
    transfer_id             BIGINT NOT NULL,
    group_target_id         BIGINT NOT NULL,
    source_group_id         BIGINT NOT NULL,
    target_id               BIGINT NOT NULL,
    cabinet_id              VARCHAR(128) NOT NULL
                            CHECK (char_length(cabinet_id) > 0),

    status                  TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN (
                                'pending', 'prepared', 'rejected', 'unresolved'
                            )),
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),
    outcome_code            VARCHAR(128)
                            CHECK (
                                outcome_code IS NULL
                                OR char_length(outcome_code) > 0
                            ),
    outcome_item_position   INTEGER
                            CHECK (
                                outcome_item_position IS NULL
                                OR outcome_item_position > 0
                            ),
    outcome_field           VARCHAR(128)
                            CHECK (
                                outcome_field IS NULL
                                OR char_length(outcome_field) > 0
                            ),
    proposal_root           BYTEA
                            CHECK (
                                proposal_root IS NULL
                                OR octet_length(proposal_root) = 32
                            ),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, group_target_id),
    UNIQUE (preparation_id, group_target_id),
    UNIQUE (transfer_id, source_group_id, group_target_id, id),

    FOREIGN KEY (transfer_id, preparation_id)
        REFERENCES wb.card_preparations (transfer_id, id),
    FOREIGN KEY (transfer_id, source_group_id, group_target_id)
        REFERENCES wb.transfer_group_targets (
            transfer_id,
            source_group_id,
            id
        ),
    FOREIGN KEY (transfer_id, target_id)
        REFERENCES wb.transfer_targets (transfer_id, id),

    CHECK (
        (
            status = 'pending'
            AND outcome_code IS NULL
            AND proposal_root IS NULL
        )
        OR (
            status = 'prepared'
            AND outcome_code = 'prepared'
            AND proposal_root IS NOT NULL
        )
        OR (
            status IN ('rejected', 'unresolved')
            AND outcome_code IS NOT NULL
            AND proposal_root IS NULL
        )
    ),
    CHECK (
        (status = 'pending' AND finished_at IS NULL)
        OR (
            status IN ('prepared', 'rejected', 'unresolved')
            AND finished_at IS NOT NULL
        )
    ),
    CHECK (started_at >= created_at),
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (updated_at >= created_at)
);

CREATE TABLE wb.card_metadata_snapshots (
    id                      BIGSERIAL PRIMARY KEY,

    preparation_group_id    BIGINT NOT NULL UNIQUE,
    transfer_id             BIGINT NOT NULL,
    metadata_digest         BYTEA NOT NULL
                            CHECK (octet_length(metadata_digest) = 32),
    snapshot                JSONB NOT NULL
                            CHECK (jsonb_typeof(snapshot) = 'object'),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, preparation_group_id, id),

    FOREIGN KEY (transfer_id, preparation_group_id)
        REFERENCES wb.card_preparation_groups (transfer_id, id)
);


CREATE TABLE wb.card_preparation_payloads (
    id                      BIGSERIAL PRIMARY KEY,

    preparation_group_id    BIGINT NOT NULL UNIQUE,
    transfer_id             BIGINT NOT NULL,
    metadata_snapshot_id    BIGINT NOT NULL,
    subject_id              BIGINT NOT NULL
                            CHECK (subject_id > 0),
    semantic_digest         BYTEA NOT NULL
                            CHECK (octet_length(semantic_digest) = 32),
    metadata_digest         BYTEA NOT NULL
                            CHECK (octet_length(metadata_digest) = 32),
    proposal_root           BYTEA NOT NULL
                            CHECK (octet_length(proposal_root) = 32),
    request_payload         BYTEA NOT NULL
                            CHECK (
                                octet_length(request_payload) > 0
                                AND octet_length(request_payload) <= 10000000
                            ),
    limits_free             BIGINT NOT NULL
                            CHECK (limits_free >= 0),
    limits_paid             BIGINT NOT NULL
                            CHECK (limits_paid >= 0),
    limits_observed_at      TIMESTAMPTZ NOT NULL,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, proposal_root),

    FOREIGN KEY (transfer_id, preparation_group_id)
        REFERENCES wb.card_preparation_groups (transfer_id, id),
    FOREIGN KEY (
        transfer_id,
        preparation_group_id,
        metadata_snapshot_id
    ) REFERENCES wb.card_metadata_snapshots (
        transfer_id,
        preparation_group_id,
        id
    )
);


CREATE TABLE wb.card_preparation_items (
    transfer_id             BIGINT NOT NULL,
    preparation_group_id    BIGINT NOT NULL,
    source_group_id         BIGINT NOT NULL,
    group_target_id         BIGINT NOT NULL,
    transfer_item_id        BIGINT NOT NULL,
    item_position           INTEGER NOT NULL
                            CHECK (item_position > 0),
    vendor_code             TEXT NOT NULL
                            CHECK (char_length(vendor_code) > 0),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (
        transfer_id,
        preparation_group_id,
        transfer_item_id
    ),

    FOREIGN KEY (
        transfer_id,
        source_group_id,
        group_target_id,
        preparation_group_id
    ) REFERENCES wb.card_preparation_groups (
        transfer_id,
        source_group_id,
        group_target_id,
        id
    ),
    FOREIGN KEY (transfer_id, source_group_id, transfer_item_id)
        REFERENCES wb.transfer_group_members (
            transfer_id,
            source_group_id,
            transfer_item_id
        )
);


CREATE FUNCTION wb.reject_card_preparation_artifact_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'immutable card preparation artifact cannot be changed'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER card_metadata_snapshots_reject_mutation
BEFORE UPDATE OR DELETE ON wb.card_metadata_snapshots
FOR EACH ROW EXECUTE FUNCTION wb.reject_card_preparation_artifact_mutation();

CREATE TRIGGER card_preparation_payloads_reject_mutation
BEFORE UPDATE OR DELETE ON wb.card_preparation_payloads
FOR EACH ROW EXECUTE FUNCTION wb.reject_card_preparation_artifact_mutation();

CREATE TRIGGER card_preparation_items_reject_mutation
BEFORE UPDATE OR DELETE ON wb.card_preparation_items
FOR EACH ROW EXECUTE FUNCTION wb.reject_card_preparation_artifact_mutation();

CREATE FUNCTION wb.protect_card_preparation_work_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.preparation_id,
        NEW.transfer_id,
        NEW.group_target_id,
        NEW.source_group_id,
        NEW.target_id,
        NEW.cabinet_id,
        NEW.created_at,
        NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.preparation_id,
        OLD.transfer_id,
        OLD.group_target_id,
        OLD.source_group_id,
        OLD.target_id,
        OLD.cabinet_id,
        OLD.created_at,
        OLD.started_at
    ) THEN
        RAISE EXCEPTION 'card preparation work identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER card_preparation_groups_protect_identity
BEFORE UPDATE ON wb.card_preparation_groups
FOR EACH ROW EXECUTE FUNCTION wb.protect_card_preparation_work_identity();

CREATE FUNCTION wb.protect_card_preparation_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.expected_group_targets,
        NEW.batch_schema_version,
        NEW.batch_normalization_version,
        NEW.batch_checksum,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.expected_group_targets,
        OLD.batch_schema_version,
        OLD.batch_normalization_version,
        OLD.batch_checksum,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'card preparation identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER card_preparations_protect_identity
BEFORE UPDATE ON wb.card_preparations
FOR EACH ROW EXECUTE FUNCTION wb.protect_card_preparation_identity();


CREATE FUNCTION wb.reject_activated_transfer_derived_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    owner_transfer_id BIGINT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        owner_transfer_id := NEW.transfer_id;
    ELSE
        owner_transfer_id := OLD.transfer_id;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM wb.transfers AS transfer
        WHERE transfer.id = owner_transfer_id
          AND transfer.phase <> 'initializing'
    ) THEN
        RAISE EXCEPTION 'activated transfer source rows are immutable'
            USING ERRCODE = '55000';
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER transfer_groups_reject_activated_mutation
BEFORE INSERT OR UPDATE OR DELETE ON wb.transfer_groups
FOR EACH ROW EXECUTE FUNCTION wb.reject_activated_transfer_derived_mutation();

CREATE TRIGGER transfer_items_reject_activated_mutation
BEFORE INSERT OR UPDATE OR DELETE ON wb.transfer_items
FOR EACH ROW EXECUTE FUNCTION wb.reject_activated_transfer_derived_mutation();

CREATE TRIGGER transfer_group_members_reject_activated_mutation
BEFORE INSERT OR UPDATE OR DELETE ON wb.transfer_group_members
FOR EACH ROW EXECUTE FUNCTION wb.reject_activated_transfer_derived_mutation();

CREATE TRIGGER transfer_group_targets_reject_activated_membership
BEFORE INSERT OR DELETE ON wb.transfer_group_targets
FOR EACH ROW EXECUTE FUNCTION wb.reject_activated_transfer_derived_mutation();

CREATE TRIGGER transfer_item_targets_reject_activated_membership
BEFORE INSERT OR DELETE ON wb.transfer_item_targets
FOR EACH ROW EXECUTE FUNCTION wb.reject_activated_transfer_derived_mutation();

CREATE FUNCTION wb.protect_transfer_group_target_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.source_group_id,
        NEW.target_id,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.source_group_id,
        OLD.target_id,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'transfer group-target identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER transfer_group_targets_protect_identity
BEFORE UPDATE ON wb.transfer_group_targets
FOR EACH ROW EXECUTE FUNCTION wb.protect_transfer_group_target_identity();

CREATE FUNCTION wb.protect_transfer_item_target_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.source_group_id,
        NEW.transfer_item_id,
        NEW.group_target_id,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.source_group_id,
        OLD.transfer_item_id,
        OLD.group_target_id,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'transfer item-target identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER transfer_item_targets_protect_identity
BEFORE UPDATE ON wb.transfer_item_targets
FOR EACH ROW EXECUTE FUNCTION wb.protect_transfer_item_target_identity();


CREATE FUNCTION wb.reject_transfer_target_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'immutable transfer target snapshot cannot be changed'
        USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION wb.protect_transfer_frozen_fields()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.batch_id,
        NEW.batch_schema_version,
        NEW.batch_normalization_version,
        NEW.batch_checksum,
        NEW.items_count,
        NEW.groups_count,
        NEW.cohort_name,
        NEW.target_snapshot_revision,
        NEW.target_set_root,
        NEW.targets_count,
        NEW.item_targets_count,
        NEW.group_targets_count,
        NEW.created_at,
        NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.batch_id,
        OLD.batch_schema_version,
        OLD.batch_normalization_version,
        OLD.batch_checksum,
        OLD.items_count,
        OLD.groups_count,
        OLD.cohort_name,
        OLD.target_snapshot_revision,
        OLD.target_set_root,
        OLD.targets_count,
        OLD.item_targets_count,
        OLD.group_targets_count,
        OLD.created_at,
        OLD.started_at
    ) THEN
        RAISE EXCEPTION 'immutable transfer inputs cannot be changed'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER transfers_protect_frozen_fields
BEFORE UPDATE ON wb.transfers
FOR EACH ROW EXECUTE FUNCTION wb.protect_transfer_frozen_fields();

CREATE TRIGGER transfer_targets_reject_mutation
BEFORE UPDATE OR DELETE ON wb.transfer_targets
FOR EACH ROW EXECUTE FUNCTION wb.reject_transfer_target_mutation();

CREATE TRIGGER transfer_targets_reject_activated_insert
BEFORE INSERT ON wb.transfer_targets
FOR EACH ROW EXECUTE FUNCTION wb.reject_activated_transfer_derived_mutation();


CREATE TABLE wb.publication_plans (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL
                            REFERENCES wb.transfers(id),
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),
    plan_digest             BYTEA NOT NULL
                            CHECK (octet_length(plan_digest) = 32),
    target_set_root         BYTEA NOT NULL
                            CHECK (octet_length(target_set_root) = 32),
    state                   TEXT NOT NULL DEFAULT 'planned'
                            CHECK (state IN (
                                'planned', 'awaiting_authorization',
                                'authorized', 'executing', 'terminal',
                                'superseded'
                            )),
    outcome_class           TEXT
                            CHECK (
                                outcome_class IS NULL
                                OR outcome_class IN (
                                    'success', 'skipped', 'rejected', 'partial',
                                    'unresolved', 'internal_error'
                                )
                            ),
    outcome_code            VARCHAR(128)
                            CHECK (
                                outcome_code IS NULL
                                OR char_length(outcome_code) > 0
                            ),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at              TIMESTAMPTZ,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, plan_digest),

    CHECK (
        (
            state IN (
                'planned', 'awaiting_authorization', 'authorized', 'executing'
            )
            AND outcome_class IS NULL
            AND outcome_code IS NULL
            AND finished_at IS NULL
        )
        OR (
            state IN ('terminal', 'superseded')
            AND outcome_class IS NOT NULL
            AND outcome_code IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CHECK (started_at IS NULL OR started_at >= created_at),
    CHECK (finished_at IS NULL OR finished_at >= COALESCE(started_at, created_at)),
    CHECK (updated_at >= created_at)
);

CREATE INDEX publication_plans_transfer_state_id_idx
ON wb.publication_plans (transfer_id, state, id);


CREATE TABLE wb.publication_actions (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    plan_id                 BIGINT NOT NULL,
    target_id               BIGINT NOT NULL,
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),
    action_key              BYTEA NOT NULL
                            CHECK (octet_length(action_key) = 32),
    kind                    TEXT NOT NULL
                            CHECK (kind IN (
                                'create_group', 'add_to_group', 'upload_media'
                            )),
    state                   TEXT NOT NULL DEFAULT 'planned'
                            CHECK (state IN (
                                'planned', 'authorized', 'dispatching',
                                'reconciling', 'terminal', 'superseded'
                            )),
    outcome_class           TEXT
                            CHECK (
                                outcome_class IS NULL
                                OR outcome_class IN (
                                    'success', 'skipped', 'rejected', 'partial',
                                    'unresolved', 'internal_error'
                                )
                            ),
    outcome_code            VARCHAR(128)
                            CHECK (
                                outcome_code IS NULL
                                OR char_length(outcome_code) > 0
                            ),
    request_digest          BYTEA NOT NULL
                            CHECK (octet_length(request_digest) = 32),
    request_payload         BYTEA NOT NULL
                            CHECK (
                                octet_length(request_payload) > 0
                                AND octet_length(request_payload) <= 10000000
                            ),
    member_set_digest       BYTEA NOT NULL
                            CHECK (octet_length(member_set_digest) = 32),
    media_link_set_root     BYTEA
                            CHECK (
                                media_link_set_root IS NULL
                                OR octet_length(media_link_set_root) = 32
                            ),
    authorization_id        BIGINT
                            CHECK (
                                authorization_id IS NULL
                                OR authorization_id > 0
                            ),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at              TIMESTAMPTZ,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, target_id, id),
    UNIQUE (transfer_id, plan_id, id),
    UNIQUE (transfer_id, plan_id, action_key),

    FOREIGN KEY (transfer_id, plan_id)
        REFERENCES wb.publication_plans (transfer_id, id),
    FOREIGN KEY (transfer_id, target_id)
        REFERENCES wb.transfer_targets (transfer_id, id),

    CHECK (
        (kind = 'upload_media' AND media_link_set_root IS NOT NULL)
        OR (
            kind IN ('create_group', 'add_to_group')
            AND media_link_set_root IS NULL
        )
    ),
    CHECK (
        (
            state IN ('planned', 'authorized', 'dispatching', 'reconciling')
            AND outcome_class IS NULL
            AND outcome_code IS NULL
            AND finished_at IS NULL
        )
        OR (
            state IN ('terminal', 'superseded')
            AND outcome_class IS NOT NULL
            AND outcome_code IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CHECK (started_at IS NULL OR started_at >= created_at),
    CHECK (finished_at IS NULL OR finished_at >= COALESCE(started_at, created_at)),
    CHECK (updated_at >= created_at)
);

CREATE INDEX publication_actions_state_id_idx
ON wb.publication_actions (state, id);

CREATE INDEX publication_actions_target_state_id_idx
ON wb.publication_actions (transfer_id, target_id, state, id);


ALTER TABLE wb.transfer_group_targets
ADD CONSTRAINT transfer_group_targets_transfer_target_id_unique
UNIQUE (transfer_id, target_id, id);


CREATE TABLE wb.publication_action_members (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    action_id               BIGINT NOT NULL,
    target_id               BIGINT NOT NULL,
    group_target_id         BIGINT NOT NULL,
    transfer_item_target_id BIGINT NOT NULL,
    request_member_index    INTEGER NOT NULL
                            CHECK (request_member_index >= 0),
    vendor_code             TEXT COLLATE "C" NOT NULL
                            CHECK (char_length(vendor_code) > 0),
    outcome_class           TEXT
                            CHECK (
                                outcome_class IS NULL
                                OR outcome_class IN (
                                    'success', 'skipped', 'rejected', 'partial',
                                    'unresolved', 'internal_error'
                                )
                            ),
    outcome_code            VARCHAR(128)
                            CHECK (
                                outcome_code IS NULL
                                OR char_length(outcome_code) > 0
                            ),
    nm_id                   BIGINT
                            CHECK (nm_id IS NULL OR nm_id > 0),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, action_id, id),
    UNIQUE (action_id, request_member_index),
    UNIQUE (action_id, transfer_item_target_id),

    FOREIGN KEY (transfer_id, target_id, action_id)
        REFERENCES wb.publication_actions (transfer_id, target_id, id),
    FOREIGN KEY (transfer_id, target_id, group_target_id)
        REFERENCES wb.transfer_group_targets (transfer_id, target_id, id),
    FOREIGN KEY (
        transfer_id,
        group_target_id,
        transfer_item_target_id
    ) REFERENCES wb.transfer_item_targets (
        transfer_id,
        group_target_id,
        id
    ),

    CHECK (
        (
            outcome_class IS NULL
            AND outcome_code IS NULL
            AND nm_id IS NULL
            AND finished_at IS NULL
        )
        OR (
            outcome_class IS NOT NULL
            AND outcome_code IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CHECK (finished_at IS NULL OR finished_at >= created_at),
    CHECK (updated_at >= created_at)
);


CREATE TABLE wb.publication_attempts (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    action_id               BIGINT NOT NULL,
    authorization_id        BIGINT NOT NULL
                            CHECK (authorization_id > 0),
    request_digest          BYTEA NOT NULL
                            CHECK (octet_length(request_digest) = 32),
    delivery_state          TEXT NOT NULL DEFAULT 'not_dispatched'
                            CHECK (delivery_state IN (
                                'not_dispatched', 'response_received',
                                'unknown_delivery'
                            )),
    http_status             INTEGER
                            CHECK (
                                http_status IS NULL
                                OR http_status BETWEEN 100 AND 599
                            ),
    response_disposition    TEXT
                            CHECK (
                                response_disposition IS NULL
                                OR response_disposition IN (
                                    'accepted', 'rejected_proven', 'uncertain'
                                )
                            ),
    classifier_version      INTEGER
                            CHECK (
                                classifier_version IS NULL
                                OR classifier_version > 0
                            ),
    safe_request_id         VARCHAR(256)
                            CHECK (
                                safe_request_id IS NULL
                                OR char_length(safe_request_id) > 0
                            ),
    safe_error_code         VARCHAR(128)
                            CHECK (
                                safe_error_code IS NULL
                                OR char_length(safe_error_code) > 0
                            ),
    unmatched_count         INTEGER NOT NULL DEFAULT 0
                            CHECK (unmatched_count >= 0),

    started_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at             TIMESTAMPTZ,

    UNIQUE (transfer_id, id),
    UNIQUE (action_id),
    UNIQUE (transfer_id, action_id),

    FOREIGN KEY (transfer_id, action_id)
        REFERENCES wb.publication_actions (transfer_id, id),

    CHECK (
        (
            delivery_state = 'not_dispatched'
            AND http_status IS NULL
            AND response_disposition IS NULL
            AND finished_at IS NULL
        )
        OR (
            delivery_state = 'response_received'
            AND http_status IS NOT NULL
            AND response_disposition IS NOT NULL
            AND classifier_version IS NOT NULL
            AND finished_at IS NOT NULL
        )
        OR (
            delivery_state = 'unknown_delivery'
            AND http_status IS NULL
            AND response_disposition = 'uncertain'
            AND classifier_version IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CHECK (finished_at IS NULL OR finished_at >= started_at)
);


ALTER TABLE wb.transfer_item_targets
ADD CONSTRAINT transfer_item_targets_source_action_fk
FOREIGN KEY (transfer_id, source_action_id)
REFERENCES wb.publication_actions (transfer_id, id);


CREATE FUNCTION wb.protect_publication_plan_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.plan_digest,
        NEW.target_set_root,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.plan_digest,
        OLD.target_set_root,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'publication plan identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_plans_protect_identity
BEFORE UPDATE ON wb.publication_plans
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_plan_identity();


CREATE FUNCTION wb.protect_publication_action_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.plan_id,
        NEW.target_id,
        NEW.action_key,
        NEW.kind,
        NEW.request_digest,
        NEW.request_payload,
        NEW.member_set_digest,
        NEW.media_link_set_root,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.plan_id,
        OLD.target_id,
        OLD.action_key,
        OLD.kind,
        OLD.request_digest,
        OLD.request_payload,
        OLD.member_set_digest,
        OLD.media_link_set_root,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'publication action identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_actions_protect_identity
BEFORE UPDATE ON wb.publication_actions
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_action_identity();


CREATE FUNCTION wb.protect_publication_action_member_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.action_id,
        NEW.target_id,
        NEW.group_target_id,
        NEW.transfer_item_target_id,
        NEW.request_member_index,
        NEW.vendor_code,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.action_id,
        OLD.target_id,
        OLD.group_target_id,
        OLD.transfer_item_target_id,
        OLD.request_member_index,
        OLD.vendor_code,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'publication action member identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_action_members_protect_identity
BEFORE UPDATE ON wb.publication_action_members
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_action_member_identity();


CREATE FUNCTION wb.protect_publication_attempt_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.action_id,
        NEW.authorization_id,
        NEW.request_digest,
        NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.action_id,
        OLD.authorization_id,
        OLD.request_digest,
        OLD.started_at
    ) THEN
        RAISE EXCEPTION 'publication attempt identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_attempts_protect_identity
BEFORE UPDATE ON wb.publication_attempts
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_attempt_identity();


CREATE FUNCTION wb.reject_publication_fact_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'publication facts cannot be deleted'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER publication_plans_reject_delete
BEFORE DELETE ON wb.publication_plans
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_actions_reject_delete
BEFORE DELETE ON wb.publication_actions
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_action_members_reject_delete
BEFORE DELETE ON wb.publication_action_members
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_attempts_reject_delete
BEFORE DELETE ON wb.publication_attempts
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();


CREATE VIEW wb.statistics_transfer_facts AS
SELECT
    transfer.id AS transfer_id,
    transfer.batch_id,
    transfer.phase,
    transfer.outcome,
    transfer.attention_code,
    transfer.cohort_name,
    transfer.items_count,
    transfer.groups_count,
    transfer.targets_count,
    transfer.item_targets_count,
    transfer.group_targets_count,
    transfer.created_at,
    transfer.started_at,
    transfer.finished_at
FROM wb.transfers AS transfer;


CREATE VIEW wb.statistics_item_facts AS
SELECT
    item_target.transfer_id,
    item_target.id AS item_target_id,
    item_target.group_target_id,
    item_target.transfer_item_id,
    target.id AS target_id,
    target.position AS target_position,
    target.cabinet_id,
    item.position AS item_position,
    item.vendor_code,
    item_target.state,
    item_target.outcome_class,
    item_target.outcome_code,
    item_target.nm_id,
    item_target.source_action_id,
    item_target.created_at,
    item_target.started_at,
    item_target.finished_at
FROM wb.transfer_item_targets AS item_target
JOIN wb.transfer_group_targets AS group_target
  ON group_target.transfer_id = item_target.transfer_id
 AND group_target.id = item_target.group_target_id
JOIN wb.transfer_targets AS target
  ON target.transfer_id = group_target.transfer_id
 AND target.id = group_target.target_id
JOIN wb.transfer_items AS item
  ON item.transfer_id = item_target.transfer_id
 AND item.id = item_target.transfer_item_id;


CREATE VIEW wb.statistics_action_facts AS
SELECT
    action.transfer_id,
    action.id AS action_id,
    action.plan_id,
    action.target_id,
    target.position AS target_position,
    target.cabinet_id,
    action.kind,
    action.state,
    action.outcome_class,
    action.outcome_code,
    action.authorization_id,
    action.created_at,
    action.started_at,
    action.finished_at
FROM wb.publication_actions AS action
JOIN wb.transfer_targets AS target
  ON target.transfer_id = action.transfer_id
 AND target.id = action.target_id;


CREATE VIEW wb.statistics_attempt_facts AS
SELECT
    attempt.transfer_id,
    attempt.id AS attempt_id,
    attempt.action_id,
    action.plan_id,
    action.target_id,
    target.position AS target_position,
    target.cabinet_id,
    action.kind AS action_kind,
    attempt.delivery_state,
    attempt.http_status,
    attempt.response_disposition,
    attempt.classifier_version,
    attempt.safe_error_code,
    attempt.unmatched_count,
    attempt.started_at,
    attempt.finished_at
FROM wb.publication_attempts AS attempt
JOIN wb.publication_actions AS action
  ON action.transfer_id = attempt.transfer_id
 AND action.id = attempt.action_id
JOIN wb.transfer_targets AS target
  ON target.transfer_id = action.transfer_id
 AND target.id = action.target_id;
