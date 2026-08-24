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

CREATE UNIQUE INDEX transfer_live_authorizations_one_open_idx
ON wb.transfer_live_authorizations (transfer_id)
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


