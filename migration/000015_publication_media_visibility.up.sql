-- HTTP submission evidence remains immutable in publication_attempts.
-- Visibility checks have an independent durable schedule and claim revision.
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
