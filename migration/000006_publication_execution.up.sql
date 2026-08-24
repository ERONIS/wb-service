CREATE TABLE wb.publication_error_cursors (
    cabinet_id              VARCHAR(128) PRIMARY KEY
                            CHECK (char_length(cabinet_id) > 0),
    cursor_updated_at       TIMESTAMPTZ,
    cursor_batch_uuid       VARCHAR(128) NOT NULL DEFAULT '',
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),
    polled_at               TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (cursor_updated_at IS NULL AND cursor_batch_uuid = '')
        OR cursor_updated_at IS NOT NULL
    ),
    CHECK (updated_at >= created_at)
);


CREATE TABLE wb.publication_error_batches (
    id                      BIGSERIAL PRIMARY KEY,

    cabinet_id              VARCHAR(128) NOT NULL
                            CHECK (char_length(cabinet_id) > 0),
    batch_uuid              VARCHAR(128) NOT NULL
                            CHECK (char_length(batch_uuid) > 0),
    batch_updated_at        TIMESTAMPTZ NOT NULL,
    source_digest           BYTEA NOT NULL
                            CHECK (octet_length(source_digest) = 32),
    vendor_codes            TEXT[] NOT NULL,
    rejected_vendor_codes   TEXT[] NOT NULL,
    error_codes             TEXT[] NOT NULL
                            CHECK (cardinality(error_codes) > 0),
    observed_at             TIMESTAMPTZ NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (cabinet_id, batch_uuid, source_digest),

    FOREIGN KEY (cabinet_id)
        REFERENCES wb.publication_error_cursors (cabinet_id),

    CHECK (observed_at <= created_at + INTERVAL '5 minutes')
);

CREATE INDEX publication_error_batches_correlation_idx
ON wb.publication_error_batches (
    cabinet_id,
    batch_updated_at,
    id
);

CREATE INDEX publication_error_batches_rejected_vendor_codes_idx
ON wb.publication_error_batches USING GIN (rejected_vendor_codes);


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
    recheck_observation_id  BIGINT NOT NULL
                            CHECK (recheck_observation_id > 0),
    baseline_cabinet_id     VARCHAR(128) NOT NULL
                            CHECK (char_length(baseline_cabinet_id) > 0),
    baseline_cursor_revision BIGINT NOT NULL
                            CHECK (baseline_cursor_revision >= 0),
    baseline_cursor_updated_at TIMESTAMPTZ,
    baseline_cursor_batch_uuid VARCHAR(128) NOT NULL DEFAULT '',
    baseline_captured_at    TIMESTAMPTZ NOT NULL,
    request_digest          BYTEA NOT NULL
                            CHECK (octet_length(request_digest) = 32),
    request_payload         BYTEA NOT NULL
                            CHECK (
                                octet_length(request_payload) > 0
                                AND octet_length(request_payload) <= 10000000
                            ),
    attribution_id          BIGINT
                            CHECK (
                                attribution_id IS NULL
                                OR attribution_id > 0
                            ),
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

    FOREIGN KEY (transfer_id, action_id, authorization_id)
        REFERENCES wb.publication_actions (
            transfer_id,
            id,
            authorization_id
        ),
    FOREIGN KEY (transfer_id, recheck_observation_id)
        REFERENCES wb.publication_observations (transfer_id, id),
    FOREIGN KEY (baseline_cabinet_id)
        REFERENCES wb.publication_error_cursors (cabinet_id),

    CHECK (
        (baseline_cursor_updated_at IS NULL AND baseline_cursor_batch_uuid = '')
        OR baseline_cursor_updated_at IS NOT NULL
    ),
    CHECK (
        (
            delivery_state = 'not_dispatched'
            AND http_status IS NULL
            AND (
                (response_disposition IS NULL AND finished_at IS NULL)
                OR (
                    response_disposition = 'rejected_proven'
                    AND classifier_version IS NOT NULL
                    AND finished_at IS NOT NULL
                )
            )
        )
        OR (
            delivery_state = 'response_received'
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


CREATE TABLE wb.publication_error_correlations (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    action_id               BIGINT NOT NULL,
    action_member_id        BIGINT NOT NULL,
    attempt_id              BIGINT NOT NULL,
    error_batch_id          BIGINT NOT NULL,
    safe_result_code        VARCHAR(128) NOT NULL
                            CHECK (char_length(safe_result_code) > 0),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, action_member_id, error_batch_id),

    FOREIGN KEY (transfer_id, action_id, action_member_id)
        REFERENCES wb.publication_action_members (
            transfer_id,
            action_id,
            id
        ),
    FOREIGN KEY (transfer_id, attempt_id)
        REFERENCES wb.publication_attempts (transfer_id, id),
    FOREIGN KEY (error_batch_id)
        REFERENCES wb.publication_error_batches (id)
);


CREATE TABLE wb.publication_attributions (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    group_target_id         BIGINT NOT NULL,
    action_id               BIGINT NOT NULL,
    action_member_id        BIGINT NOT NULL,
    cabinet_id              VARCHAR(128) NOT NULL
                            CHECK (char_length(cabinet_id) > 0),
    nm_id                   BIGINT NOT NULL
                            CHECK (nm_id > 0),
    plan_digest             BYTEA NOT NULL
                            CHECK (octet_length(plan_digest) = 32),
    attempt_id              BIGINT,
    preflight_observation_id BIGINT NOT NULL,
    post_observation_id     BIGINT,
    level                   TEXT NOT NULL
                            CHECK (level IN (
                                'direct', 'observed_after_attempt', 'ambiguous'
                            )),
    direct_correlation_key  VARCHAR(256)
                            CHECK (
                                direct_correlation_key IS NULL
                                OR char_length(direct_correlation_key) > 0
                            ),
    safe_reason_code        VARCHAR(128) NOT NULL
                            CHECK (char_length(safe_reason_code) > 0),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, action_member_id),

    FOREIGN KEY (transfer_id, action_id, action_member_id)
        REFERENCES wb.publication_action_members (
            transfer_id,
            action_id,
            id
        ),
    FOREIGN KEY (transfer_id, group_target_id)
        REFERENCES wb.transfer_group_targets (transfer_id, id),
    FOREIGN KEY (transfer_id, attempt_id)
        REFERENCES wb.publication_attempts (transfer_id, id),
    FOREIGN KEY (transfer_id, preflight_observation_id)
        REFERENCES wb.publication_observations (transfer_id, id),
    FOREIGN KEY (transfer_id, post_observation_id)
        REFERENCES wb.publication_observations (transfer_id, id),

    CHECK (
        (level = 'direct' AND direct_correlation_key IS NOT NULL)
        OR (level <> 'direct' AND direct_correlation_key IS NULL)
    )
);


CREATE TABLE wb.publication_manual_resolutions (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    action_id               BIGINT NOT NULL,
    action_member_id        BIGINT NOT NULL,
    kind                    TEXT NOT NULL
                            CHECK (kind IN (
                                'mark_remote_present',
                                'mark_rejected',
                                'close_unresolved_no_retry'
                            )),
    idempotency_key         VARCHAR(160) NOT NULL
                            CHECK (char_length(idempotency_key) > 0),
    command_digest          BYTEA NOT NULL
                            CHECK (octet_length(command_digest) = 32),
    actor_id                BIGINT NOT NULL
                            CHECK (actor_id > 0),
    actor_digest            BYTEA NOT NULL
                            CHECK (octet_length(actor_digest) = 32),
    actor_name              VARCHAR(256) NOT NULL
                            CHECK (char_length(actor_name) > 0),
    expected_action_revision BIGINT NOT NULL
                            CHECK (expected_action_revision >= 0),
    result_action_revision  BIGINT NOT NULL
                            CHECK (
                                result_action_revision
                                = expected_action_revision + 1
                            ),
    evidence_observation_id BIGINT,
    evidence_error_batch_id BIGINT,
    result_outcome_class    TEXT NOT NULL
                            CHECK (result_outcome_class IN (
                                'success', 'rejected', 'unresolved',
                                'internal_error'
                            )),
    result_outcome_code     VARCHAR(128) NOT NULL
                            CHECK (char_length(result_outcome_code) > 0),
    nm_id                   BIGINT
                            CHECK (nm_id IS NULL OR nm_id > 0),
    imt_id                  BIGINT
                            CHECK (imt_id IS NULL OR imt_id > 0),
    subject_id              BIGINT
                            CHECK (subject_id IS NULL OR subject_id > 0),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (idempotency_key),

    FOREIGN KEY (transfer_id, action_id, action_member_id)
        REFERENCES wb.publication_action_members (
            transfer_id,
            action_id,
            id
        ),
    FOREIGN KEY (transfer_id, evidence_observation_id)
        REFERENCES wb.publication_observations (transfer_id, id),
    FOREIGN KEY (evidence_error_batch_id)
        REFERENCES wb.publication_error_batches (id),

    CHECK (
        (
            kind = 'mark_remote_present'
            AND evidence_observation_id IS NOT NULL
            AND evidence_error_batch_id IS NULL
            AND result_outcome_class = 'success'
            AND nm_id IS NOT NULL
            AND imt_id IS NOT NULL
            AND subject_id IS NOT NULL
        )
        OR (
            kind = 'mark_rejected'
            AND evidence_observation_id IS NULL
            AND evidence_error_batch_id IS NOT NULL
            AND result_outcome_class = 'rejected'
            AND nm_id IS NULL
            AND imt_id IS NULL
            AND subject_id IS NULL
        )
        OR (
            kind = 'close_unresolved_no_retry'
            AND evidence_observation_id IS NULL
            AND evidence_error_batch_id IS NULL
            AND result_outcome_class IN ('unresolved', 'internal_error')
            AND nm_id IS NULL
            AND imt_id IS NULL
            AND subject_id IS NULL
        )
    )
);

CREATE INDEX publication_manual_resolutions_member_idx
ON wb.publication_manual_resolutions (
    transfer_id,
    action_id,
    action_member_id,
    id
);


ALTER TABLE wb.publication_attempts
ADD CONSTRAINT publication_attempts_attribution_fk
FOREIGN KEY (transfer_id, attribution_id)
REFERENCES wb.publication_attributions (transfer_id, id);


ALTER TABLE wb.transfer_item_targets
ADD CONSTRAINT transfer_item_targets_source_action_fk
FOREIGN KEY (transfer_id, source_action_id)
REFERENCES wb.publication_actions (transfer_id, id);


