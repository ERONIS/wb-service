CREATE TABLE wb.cabinet_copy_sessions (
    id                  BIGSERIAL PRIMARY KEY,
    author_user_id      INTEGER REFERENCES wb.users(id) ON DELETE SET NULL,
    author_telegram_id  BIGINT NOT NULL CHECK (author_telegram_id > 0),
    step                TEXT NOT NULL DEFAULT 'source'
                        CHECK (step IN (
                            'source', 'target', 'count', 'count_input',
                            'tags', 'review', 'submitted', 'cancelled'
                        )),
    revision            BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    source_cabinet_id   VARCHAR(128),
    target_cabinet_id   VARCHAR(128),
    requested_count     INTEGER CHECK (requested_count > 0),
    selected_tag_ids    BIGINT[] NOT NULL DEFAULT '{}',
    selected_tag_names  TEXT[] NOT NULL DEFAULT '{}',
    prepared_count      INTEGER NOT NULL DEFAULT 0 CHECK (prepared_count >= 0),
    batch_id            BIGINT,
    transfer_id         BIGINT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    submitted_at        TIMESTAMPTZ,
    CHECK (
        source_cabinet_id IS NULL
        OR char_length(source_cabinet_id) > 0
    ),
    CHECK (
        target_cabinet_id IS NULL
        OR char_length(target_cabinet_id) > 0
    ),
    CHECK (
        source_cabinet_id IS NULL
        OR target_cabinet_id IS NULL
        OR source_cabinet_id <> target_cabinet_id
    ),
    CHECK (cardinality(selected_tag_ids) = cardinality(selected_tag_names)),
    CHECK (
        (step = 'submitted' AND batch_id IS NOT NULL AND transfer_id IS NOT NULL AND submitted_at IS NOT NULL)
        OR (step <> 'submitted' AND submitted_at IS NULL)
    )
);

CREATE UNIQUE INDEX cabinet_copy_sessions_one_active_author_idx
ON wb.cabinet_copy_sessions (author_telegram_id)
WHERE step NOT IN ('submitted', 'cancelled');

CREATE TABLE wb.cabinet_copy_items (
    id                  BIGSERIAL PRIMARY KEY,
    session_id          BIGINT NOT NULL
                        REFERENCES wb.cabinet_copy_sessions(id)
                        ON DELETE CASCADE,
    position            INTEGER NOT NULL CHECK (position > 0),
    source_nm_id        BIGINT NOT NULL CHECK (source_nm_id > 0),
    source_imt_id       BIGINT NOT NULL CHECK (source_imt_id >= 0),
    vendor_code         TEXT NOT NULL CHECK (char_length(vendor_code) > 0),
    payload             JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (session_id, position),
    UNIQUE (session_id, source_nm_id),
    UNIQUE (session_id, vendor_code)
);

ALTER TABLE wb.card_batches
    ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'xlsx'
        CHECK (source_kind IN ('xlsx', 'wb_cabinet')),
    ADD COLUMN source_reference_id BIGINT;

UPDATE wb.card_batches
SET source_reference_id = source_session_id;

ALTER TABLE wb.card_batches
    ALTER COLUMN source_reference_id SET NOT NULL,
    ALTER COLUMN source_session_id DROP NOT NULL,
    ADD CONSTRAINT card_batches_source_origin_unique
        UNIQUE (source_kind, source_reference_id),
    ADD CONSTRAINT card_batches_source_shape_check
        CHECK (
            (source_kind = 'xlsx'
             AND source_session_id IS NOT NULL
             AND source_reference_id = source_session_id)
            OR
            (source_kind = 'wb_cabinet' AND source_session_id IS NULL)
        );

ALTER TABLE wb.card_batch_items
    ALTER COLUMN source_file_id DROP NOT NULL;

CREATE OR REPLACE FUNCTION wb.validate_card_batch_item_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM wb.card_batches AS batch
        LEFT JOIN wb.card_import_sessions AS import_session
            ON import_session.id = batch.source_session_id
        LEFT JOIN wb.card_import_files AS file
            ON file.id = NEW.source_file_id
           AND file.session_id = import_session.id
        LEFT JOIN wb.cabinet_copy_sessions AS copy_session
            ON copy_session.id = batch.source_reference_id
        WHERE batch.id = NEW.batch_id
          AND (
              (
                  batch.source_kind = 'xlsx'
                  AND import_session.status = 'collecting'
                  AND NEW.source_file_id IS NOT NULL
                  AND file.id IS NOT NULL
              )
              OR
              (
                  batch.source_kind = 'wb_cabinet'
                  AND copy_session.step = 'review'
                  AND NEW.source_file_id IS NULL
              )
          )
    ) THEN
        RAISE EXCEPTION 'card batch item source is invalid or batch is frozen'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

ALTER TABLE wb.cabinet_copy_sessions
    ADD CONSTRAINT cabinet_copy_sessions_batch_fk
        FOREIGN KEY (batch_id) REFERENCES wb.card_batches(id),
    ADD CONSTRAINT cabinet_copy_sessions_transfer_fk
        FOREIGN KEY (transfer_id) REFERENCES wb.transfers(id);

CREATE INDEX cabinet_copy_sessions_author_created_idx
ON wb.cabinet_copy_sessions (author_telegram_id, created_at DESC);

CREATE TRIGGER cabinet_copy_items_reject_mutation
BEFORE UPDATE OR DELETE ON wb.cabinet_copy_items
FOR EACH ROW EXECUTE FUNCTION wb.reject_frozen_card_batch_mutation();
