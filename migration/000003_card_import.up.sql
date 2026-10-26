CREATE TABLE wb.card_batches (
    id                         BIGSERIAL PRIMARY KEY,

    source_session_id          BIGINT NOT NULL UNIQUE
                               CHECK (source_session_id > 0),
    author_telegram_id         BIGINT NOT NULL
                               CHECK (author_telegram_id > 0),
    finalized_from_revision    BIGINT NOT NULL
                               CHECK (finalized_from_revision >= 0),
    finalize_idempotency_key   VARCHAR(128) NOT NULL
                               CHECK (char_length(finalize_idempotency_key) > 0),
    items_count                INTEGER NOT NULL
                               CHECK (items_count > 0),
    groups_count               INTEGER NOT NULL
                               CHECK (groups_count > 0 AND groups_count <= items_count),
    sealed                     BOOLEAN NOT NULL DEFAULT FALSE,
    finalized_at               TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX card_batches_finalized_cursor_idx
    ON wb.card_batches (finalized_at, id)
    WHERE sealed;

CREATE INDEX card_batches_author_idx
    ON wb.card_batches (author_telegram_id, id);


CREATE TABLE wb.card_batch_items (
    id                  BIGSERIAL PRIMARY KEY,

    batch_id            BIGINT NOT NULL
                        REFERENCES wb.card_batches(id),
    position            INTEGER NOT NULL
                        CHECK (position > 0),
    source_group_number INTEGER NOT NULL
                        CHECK (source_group_number > 0),
    payload             JSONB NOT NULL
                        CHECK (jsonb_typeof(payload) = 'object'),

    UNIQUE (batch_id, position)
);

CREATE FUNCTION wb.reject_frozen_card_batch_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME = 'card_batches' AND TG_OP = 'UPDATE' THEN
        IF NOT OLD.sealed AND NEW.sealed
           AND to_jsonb(NEW) - 'sealed' = to_jsonb(OLD) - 'sealed' THEN
            RETURN NEW;
        END IF;
    END IF;

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
        WHERE batch.id = NEW.batch_id
          AND NOT batch.sealed
    ) THEN
        RAISE EXCEPTION 'card batch is frozen or missing'
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
