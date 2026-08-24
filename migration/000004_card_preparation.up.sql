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

CREATE TABLE wb.card_preparation_artifacts (
    preparation_group_id    BIGINT PRIMARY KEY,
    transfer_id             BIGINT NOT NULL,
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
    metadata_snapshot       JSONB NOT NULL
                            CHECK (jsonb_typeof(metadata_snapshot) = 'object'),
    limits_free             BIGINT NOT NULL
                            CHECK (limits_free >= 0),
    limits_paid             BIGINT NOT NULL
                            CHECK (limits_paid >= 0),
    limits_observed_at      TIMESTAMPTZ NOT NULL,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, proposal_root),

    FOREIGN KEY (transfer_id, preparation_group_id)
        REFERENCES wb.card_preparation_groups (transfer_id, id)
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
        REFERENCES wb.transfer_items (
            transfer_id,
            source_group_id,
            id
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

CREATE TRIGGER card_preparation_artifacts_reject_mutation
BEFORE UPDATE OR DELETE ON wb.card_preparation_artifacts
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


