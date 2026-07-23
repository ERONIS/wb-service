\set ON_ERROR_STOP on

INSERT INTO wb.users (
    tg_id,
    tg_nickname,
    full_name,
    role
)
VALUES (
    :'admin_tg_id'::BIGINT,
    NULL,
    :'admin_full_name',
    'admin'
)
ON CONFLICT (tg_id)
DO UPDATE SET
    full_name = EXCLUDED.full_name,
    role = 'admin',
    updated_at = CURRENT_TIMESTAMP;
