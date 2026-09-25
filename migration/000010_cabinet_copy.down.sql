DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM wb.card_batches WHERE source_kind = 'wb_cabinet'
    ) THEN
        RAISE EXCEPTION 'cannot roll back cabinet copy migration while cabinet-copy batches exist';
    END IF;
END;
$$;

ALTER TABLE wb.cabinet_copy_sessions
    DROP CONSTRAINT cabinet_copy_sessions_transfer_fk,
    DROP CONSTRAINT cabinet_copy_sessions_batch_fk;

CREATE OR REPLACE FUNCTION wb.validate_card_batch_item_insert()
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

ALTER TABLE wb.card_batch_items
    ALTER COLUMN source_file_id SET NOT NULL;

ALTER TABLE wb.card_batches
    DROP CONSTRAINT card_batches_source_shape_check,
    DROP CONSTRAINT card_batches_source_origin_unique,
    ALTER COLUMN source_session_id SET NOT NULL,
    DROP COLUMN source_reference_id,
    DROP COLUMN source_kind;

DROP TABLE wb.cabinet_copy_items;
DROP TABLE wb.cabinet_copy_sessions;
