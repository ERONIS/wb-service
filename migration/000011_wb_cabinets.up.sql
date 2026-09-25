CREATE TABLE wb.api_cabinets (
    cabinet_id              VARCHAR(128) PRIMARY KEY
                            CHECK (
                                char_length(cabinet_id) > 0
                                AND cabinet_id = btrim(cabinet_id)
                            ),
    owner_tg_id             BIGINT NOT NULL
                            CONSTRAINT api_cabinets_owner_fk
                            REFERENCES wb.users(tg_id),
    display_name            VARCHAR(128) NOT NULL
                            CHECK (char_length(btrim(display_name)) > 0),
    normalized_name         VARCHAR(128) NOT NULL,
    token                   TEXT NOT NULL
                            CHECK (char_length(btrim(token)) > 0),
    seller_key              BYTEA NOT NULL
                            CONSTRAINT api_cabinets_seller_key_key UNIQUE
                            CHECK (octet_length(seller_key) = 32),
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
