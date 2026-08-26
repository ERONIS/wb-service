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
        OR (outcome = 'failed' AND attention_code IS NOT NULL)
        OR outcome = 'unresolved'
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
    client_generation       BYTEA NOT NULL
                            CHECK (octet_length(client_generation) = 32),
    credential_expires_at   TIMESTAMPTZ NOT NULL,
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

    CHECK (content_read AND content_write),
    CHECK (credential_expires_at > created_at)
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
    attention_closed_at     TIMESTAMPTZ,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at              TIMESTAMPTZ,
    finished_at             TIMESTAMPTZ,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, group_target_id, id),
    UNIQUE (transfer_id, transfer_item_id, group_target_id),

    FOREIGN KEY (transfer_id, source_group_id, transfer_item_id)
        REFERENCES wb.transfer_items (
            transfer_id,
            source_group_id,
            id
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
            AND attention_closed_at IS NULL
            AND started_at IS NULL
            AND finished_at IS NULL
        )
        OR (
            state = 'running'
            AND outcome_class IS NULL
            AND outcome_code IS NULL
            AND attention_closed_at IS NULL
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
    CHECK (
        attention_closed_at IS NULL
        OR (
            state = 'terminal'
            AND outcome_class IN ('unresolved', 'internal_error')
            AND attention_closed_at >= finished_at
        )
    ),
    CHECK (started_at IS NULL OR started_at >= created_at),
    CHECK (finished_at IS NULL OR finished_at >= started_at),
    CHECK (updated_at >= created_at)
);

CREATE INDEX transfer_item_targets_open_attention_idx
ON wb.transfer_item_targets (finished_at DESC, id DESC)
WHERE state = 'terminal'
  AND outcome_class IN ('unresolved', 'internal_error')
  AND attention_closed_at IS NULL
  AND source_action_id IS NOT NULL;


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

