CREATE TABLE wb.publication_product_retries (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    action_id               BIGINT NOT NULL,
    authorization_id        BIGINT NOT NULL CHECK (authorization_id > 0),
    request_digest          BYTEA NOT NULL CHECK (octet_length(request_digest) = 32),
    request_payload         BYTEA NOT NULL CHECK (
                                octet_length(request_payload) > 0
                                AND octet_length(request_payload) <= 10000000
                            ),
    delivery_state          TEXT,
    http_status             INTEGER CHECK (
                                http_status IS NULL
                                OR http_status BETWEEN 100 AND 599
                            ),
    response_disposition    TEXT CHECK (
                                response_disposition IS NULL
                                OR response_disposition IN (
                                    'accepted', 'rejected_proven', 'uncertain'
                                )
                            ),
    classifier_version      INTEGER CHECK (
                                classifier_version IS NULL
                                OR classifier_version > 0
                            ),
    safe_error_code         VARCHAR(128) CHECK (
                                safe_error_code IS NULL
                                OR char_length(safe_error_code) > 0
                            ),
    unmatched_count         INTEGER NOT NULL DEFAULT 0 CHECK (unmatched_count >= 0),

    started_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at             TIMESTAMPTZ,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, action_id),

    FOREIGN KEY (transfer_id, action_id, authorization_id)
        REFERENCES wb.publication_actions (
            transfer_id,
            id,
            authorization_id
        ),

    CHECK (
        (
            finished_at IS NULL
            AND delivery_state IS NULL
            AND http_status IS NULL
            AND response_disposition IS NULL
            AND classifier_version IS NULL
            AND safe_error_code IS NULL
        )
        OR (
            finished_at IS NOT NULL
            AND delivery_state IN (
                'not_dispatched', 'response_received', 'unknown_delivery'
            )
            AND response_disposition IS NOT NULL
            AND classifier_version IS NOT NULL
            AND safe_error_code IS NOT NULL
            AND (
                (delivery_state = 'response_received' AND http_status IS NOT NULL)
                OR (delivery_state <> 'response_received' AND http_status IS NULL)
            )
        )
    ),
    CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE FUNCTION wb.protect_publication_product_retry()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.action_id,
        NEW.authorization_id,
        NEW.request_digest,
        NEW.request_payload,
        NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.action_id,
        OLD.authorization_id,
        OLD.request_digest,
        OLD.request_payload,
        OLD.started_at
    ) THEN
        RAISE EXCEPTION 'publication product retry identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    IF OLD.finished_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'finished publication product retry is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_product_retries_protect
BEFORE UPDATE ON wb.publication_product_retries
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_product_retry();

CREATE TRIGGER publication_product_retries_reject_delete
BEFORE DELETE ON wb.publication_product_retries
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();
