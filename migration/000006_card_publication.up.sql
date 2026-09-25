CREATE TABLE wb.publication_observations (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    target_id               BIGINT NOT NULL,
    cabinet_id              VARCHAR(128) NOT NULL
                            CHECK (char_length(cabinet_id) > 0),
    kind                    TEXT NOT NULL
                            CHECK (kind IN (
                                'normal_trash_preflight',
                                'targeted_recheck',
                                'post_submission'
                            )),
    observation_digest      BYTEA NOT NULL
                            CHECK (octet_length(observation_digest) = 32),
    normal_count            INTEGER NOT NULL
                            CHECK (normal_count >= 0),
    trash_count             INTEGER NOT NULL
                            CHECK (trash_count >= 0),
    snapshot                JSONB NOT NULL
                            CHECK (jsonb_typeof(snapshot) = 'object'),
    observed_at             TIMESTAMPTZ NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, target_id, id),
    UNIQUE (transfer_id, target_id, kind, observation_digest),

    FOREIGN KEY (transfer_id, target_id)
        REFERENCES wb.transfer_targets (transfer_id, id),

    CHECK (observed_at <= created_at + INTERVAL '5 minutes')
);

CREATE INDEX publication_observations_target_observed_idx
ON wb.publication_observations (transfer_id, target_id, observed_at, id);


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
    UNIQUE (transfer_id, id, authorization_id),
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

CREATE INDEX publication_actions_plan_unfinished_idx
ON wb.publication_actions (transfer_id, plan_id)
WHERE state NOT IN ('terminal', 'superseded');


CREATE TABLE wb.product_identities (
    id                      BIGSERIAL PRIMARY KEY,

    cabinet_id              VARCHAR(128) NOT NULL
                            CHECK (char_length(cabinet_id) > 0),
    vendor_code_key         TEXT COLLATE "C" NOT NULL
                            CHECK (char_length(vendor_code_key) > 0),
    normalization_version   INTEGER NOT NULL
                            CHECK (normalization_version > 0),
    state                   TEXT NOT NULL DEFAULT 'unknown'
                            CHECK (state IN (
                                'unknown', 'mutation_pending',
                                'remote_present', 'rejected',
                                'blocked_uncertain', 'remote_missing',
                                'remote_conflict'
                            )),
    nm_id                   BIGINT
                            CHECK (nm_id IS NULL OR nm_id > 0),
    imt_id                  BIGINT
                            CHECK (imt_id IS NULL OR imt_id > 0),
    subject_id              BIGINT
                            CHECK (subject_id IS NULL OR subject_id > 0),
    observation_digest      BYTEA
                            CHECK (
                                observation_digest IS NULL
                                OR octet_length(observation_digest) = 32
                            ),
    active_transfer_id      BIGINT,
    active_action_id        BIGINT,
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (cabinet_id, vendor_code_key),

    FOREIGN KEY (active_transfer_id, active_action_id)
        REFERENCES wb.publication_actions (transfer_id, id),

    CHECK (
        (active_transfer_id IS NULL AND active_action_id IS NULL)
        OR (active_transfer_id IS NOT NULL AND active_action_id IS NOT NULL)
    ),
    CHECK (
        (state = 'mutation_pending' AND active_action_id IS NOT NULL)
        OR (state <> 'mutation_pending')
    ),
    CHECK (updated_at >= created_at)
);

CREATE INDEX product_identities_active_action_idx
ON wb.product_identities (active_transfer_id, active_action_id)
WHERE active_action_id IS NOT NULL;


CREATE TABLE wb.transfer_live_authorizations (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    plan_id                 BIGINT NOT NULL,
    plan_digest             BYTEA NOT NULL
                            CHECK (octet_length(plan_digest) = 32),
    target_set_root         BYTEA NOT NULL
                            CHECK (octet_length(target_set_root) = 32),
    revision                BIGINT NOT NULL DEFAULT 0
                            CHECK (revision >= 0),
    state                   TEXT NOT NULL DEFAULT 'requested'
                            CHECK (state IN (
                                'requested', 'authorized', 'revoked',
                                'expired', 'superseded', 'closed'
                            )),
    trusted_actor_id        BIGINT NOT NULL
                            CHECK (trusted_actor_id > 0),
    trusted_actor_digest    BYTEA NOT NULL
                            CHECK (octet_length(trusted_actor_digest) = 32),
    trusted_actor_name      VARCHAR(256) NOT NULL
                            CHECK (char_length(trusted_actor_name) > 0),
    requested_at            TIMESTAMPTZ NOT NULL,
    approved_at             TIMESTAMPTZ,
    expires_at              TIMESTAMPTZ NOT NULL,
    revoked_at              TIMESTAMPTZ,
    closed_at               TIMESTAMPTZ,
    safe_reason_code        VARCHAR(128)
                            CHECK (
                                safe_reason_code IS NULL
                                OR char_length(safe_reason_code) > 0
                            ),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (transfer_id, id),
    UNIQUE (transfer_id, plan_id, id),

    FOREIGN KEY (transfer_id, plan_id)
        REFERENCES wb.publication_plans (transfer_id, id),

    CHECK (expires_at > requested_at),
    CHECK (updated_at >= created_at),
    CHECK (
        (state = 'requested'
            AND approved_at IS NULL
            AND revoked_at IS NULL
            AND closed_at IS NULL
            AND safe_reason_code IS NULL)
        OR (state = 'authorized'
            AND approved_at IS NOT NULL
            AND revoked_at IS NULL
            AND closed_at IS NULL
            AND safe_reason_code IS NULL)
        OR (state = 'revoked'
            AND revoked_at IS NOT NULL
            AND closed_at IS NULL
            AND safe_reason_code IS NOT NULL)
        OR (state = 'expired'
            AND revoked_at IS NULL
            AND closed_at IS NULL
            AND safe_reason_code IS NOT NULL)
        OR (state IN ('superseded', 'closed')
            AND revoked_at IS NULL
            AND closed_at IS NOT NULL
            AND safe_reason_code IS NOT NULL)
    ),
    CHECK (approved_at IS NULL OR approved_at >= requested_at),
    CHECK (revoked_at IS NULL OR revoked_at >= requested_at),
    CHECK (closed_at IS NULL OR closed_at >= requested_at)
);

CREATE UNIQUE INDEX transfer_live_authorizations_one_open_plan_idx
ON wb.transfer_live_authorizations (transfer_id, plan_id)
WHERE state IN ('requested', 'authorized');

CREATE INDEX transfer_live_authorizations_plan_state_idx
ON wb.transfer_live_authorizations (transfer_id, plan_id, state, id);


CREATE TABLE wb.transfer_live_authorization_commands (
    id                      BIGSERIAL PRIMARY KEY,

    transfer_id             BIGINT NOT NULL,
    authorization_id        BIGINT NOT NULL,
    kind                    TEXT NOT NULL
                            CHECK (kind IN (
                                'request', 'approve', 'revoke', 'expire',
                                'supersede', 'close'
                            )),
    idempotency_key         VARCHAR(160) NOT NULL
                            CHECK (char_length(idempotency_key) > 0),
    actor_digest            BYTEA NOT NULL
                            CHECK (octet_length(actor_digest) = 32),
    command_digest          BYTEA NOT NULL
                            CHECK (octet_length(command_digest) = 32),
    result_state            TEXT NOT NULL
                            CHECK (result_state IN (
                                'requested', 'authorized', 'revoked',
                                'expired', 'superseded', 'closed'
                            )),
    result_revision         BIGINT NOT NULL
                            CHECK (result_revision >= 0),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE (idempotency_key),

    FOREIGN KEY (transfer_id, authorization_id)
        REFERENCES wb.transfer_live_authorizations (transfer_id, id)
);

ALTER TABLE wb.publication_actions
ADD CONSTRAINT publication_actions_live_authorization_fk
FOREIGN KEY (transfer_id, authorization_id)
REFERENCES wb.transfer_live_authorizations (transfer_id, id);


ALTER TABLE wb.transfer_group_targets
ADD COLUMN publication_plan_id BIGINT;

ALTER TABLE wb.transfer_group_targets
ADD CONSTRAINT transfer_group_targets_publication_plan_fk
FOREIGN KEY (transfer_id, publication_plan_id)
REFERENCES wb.publication_plans (transfer_id, id);

ALTER TABLE wb.transfer_group_targets
ADD CONSTRAINT transfer_group_targets_publication_plan_state_check
CHECK (
    (publication_status = 'not_started' AND publication_plan_id IS NULL)
    OR (publication_status <> 'not_started' AND publication_plan_id IS NOT NULL)
    OR (
        preparation_status IN ('rejected', 'unresolved')
        AND publication_status = 'skipped'
        AND media_status = 'skipped'
        AND publication_plan_id IS NULL
    )
);

CREATE INDEX transfer_group_targets_unplanned_prepared_idx
ON wb.transfer_group_targets (transfer_id, target_id, id)
WHERE preparation_status = 'succeeded'
  AND publication_status = 'not_started';

CREATE INDEX transfer_group_targets_publication_plan_idx
ON wb.transfer_group_targets (transfer_id, publication_plan_id, id)
WHERE publication_plan_id IS NOT NULL;


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

CREATE INDEX publication_error_correlations_error_batch_idx
ON wb.publication_error_correlations (error_batch_id, id);


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


CREATE TABLE wb.publication_media_visibility (
    transfer_id BIGINT NOT NULL,
    action_id BIGINT NOT NULL,
    attempt_id BIGINT NOT NULL,
    deadline_at TIMESTAMPTZ NOT NULL,
    next_check_at TIMESTAMPTZ NOT NULL,
    leased_until TIMESTAMPTZ,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    check_count INTEGER NOT NULL DEFAULT 0 CHECK (check_count >= 0),
    last_card_found BOOLEAN NOT NULL DEFAULT FALSE,
    last_photos_count INTEGER NOT NULL DEFAULT 0 CHECK (last_photos_count >= 0),
    last_error_code VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMPTZ,
    PRIMARY KEY (transfer_id, action_id),
    UNIQUE (attempt_id),
    FOREIGN KEY (transfer_id, action_id)
        REFERENCES wb.publication_actions (transfer_id, id),
    FOREIGN KEY (transfer_id, attempt_id)
        REFERENCES wb.publication_attempts (transfer_id, id),
    CHECK (deadline_at > created_at),
    CHECK (next_check_at <= deadline_at)
);

CREATE INDEX publication_media_visibility_due_idx
    ON wb.publication_media_visibility (next_check_at, action_id)
    WHERE finished_at IS NULL;


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


CREATE FUNCTION wb.protect_publication_observation_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'publication observation is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_observations_protect_identity
BEFORE UPDATE ON wb.publication_observations
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_observation_identity();


CREATE FUNCTION wb.protect_live_authorization_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.plan_id,
        NEW.plan_digest,
        NEW.target_set_root,
        NEW.trusted_actor_id,
        NEW.trusted_actor_digest,
        NEW.trusted_actor_name,
        NEW.requested_at,
        NEW.expires_at,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.plan_id,
        OLD.plan_digest,
        OLD.target_set_root,
        OLD.trusted_actor_id,
        OLD.trusted_actor_digest,
        OLD.trusted_actor_name,
        OLD.requested_at,
        OLD.expires_at,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'live authorization identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER transfer_live_authorizations_protect_identity
BEFORE UPDATE ON wb.transfer_live_authorizations
FOR EACH ROW EXECUTE FUNCTION wb.protect_live_authorization_identity();


CREATE FUNCTION wb.reject_live_authorization_command_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'live authorization command is immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER transfer_live_authorization_commands_reject_mutation
BEFORE UPDATE OR DELETE ON wb.transfer_live_authorization_commands
FOR EACH ROW EXECUTE FUNCTION wb.reject_live_authorization_command_mutation();


CREATE FUNCTION wb.protect_publication_error_cursor_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.cabinet_id IS DISTINCT FROM OLD.cabinet_id
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'publication error cursor identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_error_cursors_protect_identity
BEFORE UPDATE ON wb.publication_error_cursors
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_error_cursor_identity();


CREATE FUNCTION wb.reject_publication_error_fact_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'publication error evidence is immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER publication_error_batches_reject_mutation
BEFORE UPDATE OR DELETE ON wb.publication_error_batches
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_error_fact_mutation();


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
        NEW.recheck_observation_id,
        NEW.baseline_cabinet_id,
        NEW.baseline_cursor_revision,
        NEW.baseline_cursor_updated_at,
        NEW.baseline_cursor_batch_uuid,
        NEW.baseline_captured_at,
        NEW.request_digest,
        NEW.request_payload,
        NEW.attribution_id,
        NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.action_id,
        OLD.authorization_id,
        OLD.recheck_observation_id,
        OLD.baseline_cabinet_id,
        OLD.baseline_cursor_revision,
        OLD.baseline_cursor_updated_at,
        OLD.baseline_cursor_batch_uuid,
        OLD.baseline_captured_at,
        OLD.request_digest,
        OLD.request_payload,
        OLD.attribution_id,
        OLD.started_at
    ) THEN
        RAISE EXCEPTION 'publication attempt identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    IF OLD.finished_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'finished publication attempt is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_attempts_protect_identity
BEFORE UPDATE ON wb.publication_attempts
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_attempt_identity();


CREATE FUNCTION wb.protect_publication_attribution_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'publication attribution is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_attributions_protect_identity
BEFORE UPDATE ON wb.publication_attributions
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_attribution_identity();


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

CREATE TRIGGER publication_observations_reject_delete
BEFORE DELETE ON wb.publication_observations
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER transfer_live_authorizations_reject_delete
BEFORE DELETE ON wb.transfer_live_authorizations
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

CREATE TRIGGER publication_attributions_reject_delete
BEFORE DELETE ON wb.publication_attributions
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_product_retries_reject_delete
BEFORE DELETE ON wb.publication_product_retries
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_error_correlations_reject_mutation
BEFORE UPDATE OR DELETE ON wb.publication_error_correlations
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_manual_resolutions_reject_mutation
BEFORE UPDATE OR DELETE ON wb.publication_manual_resolutions
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();
