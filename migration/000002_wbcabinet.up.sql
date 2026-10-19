CREATE TABLE wb.api_cabinets (
    seller_id          VARCHAR(36) PRIMARY KEY
                            CHECK (seller_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    owner_tg_id             BIGINT NOT NULL
                            CONSTRAINT api_cabinets_owner_fk
                            REFERENCES wb.users(tg_id),
    display_name            VARCHAR(128) NOT NULL
                            CHECK (char_length(btrim(display_name)) > 0),
    normalized_name         VARCHAR(128) NOT NULL,
    token                   TEXT NOT NULL
                            CHECK (char_length(btrim(token)) > 0),
    token_properties        BIGINT NOT NULL DEFAULT 0
                            CHECK (token_properties >= 0),
    binding_revision        BIGINT NOT NULL DEFAULT 1
                            CHECK (binding_revision > 0),
    capability_revision     BIGINT NOT NULL DEFAULT 1
                            CHECK (capability_revision > 0),
    content_read            BOOLEAN NOT NULL,
    content_write           BOOLEAN NOT NULL,
    client_generation       BYTEA NOT NULL
                            CHECK (octet_length(client_generation) = 32),
    credential_expires_at   TIMESTAMPTZ NOT NULL,
    verified_at             TIMESTAMPTZ NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN (
                                'active',
                                'expired',
                                'verification_failed',
                                'identity_mismatch'
                            )),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT api_cabinets_owner_name_key
        UNIQUE (owner_tg_id, normalized_name),
    CHECK (normalized_name = lower(btrim(display_name)))
);

CREATE INDEX api_cabinets_owner_status_idx
ON wb.api_cabinets (owner_tg_id, status, display_name);
