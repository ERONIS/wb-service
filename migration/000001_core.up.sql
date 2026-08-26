CREATE SCHEMA wb;


CREATE TABLE wb.users (
    id              SERIAL PRIMARY KEY,

    tg_id           BIGINT NOT NULL UNIQUE
                    CHECK (tg_id > 0),

    tg_nickname     VARCHAR(32),

    full_name       VARCHAR(100) NOT NULL
                    CHECK (char_length(full_name) BETWEEN 3 AND 100),

    role            TEXT NOT NULL DEFAULT 'user'
                    CHECK (role IN ('user', 'admin')),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE wb.cabinet_identity_bindings (
    cabinet_id              VARCHAR(128) PRIMARY KEY
                            CHECK (char_length(cabinet_id) > 0),
    seller_key              BYTEA NOT NULL UNIQUE
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
                                'identity_mismatch'
                            )),
    mismatch_seller_key     BYTEA
                            CHECK (
                                mismatch_seller_key IS NULL
                                OR octet_length(mismatch_seller_key) = 32
                            ),

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CHECK (
        (status = 'active' AND mismatch_seller_key IS NULL)
        OR (
            status = 'identity_mismatch'
            AND mismatch_seller_key IS NOT NULL
        )
    )
);
