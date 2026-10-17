\set ON_ERROR_STOP on

-- Начальное наполнение кабинетов WB для администратора
-- Идентификатор администратора передается через параметр psql :admin_tg_id
INSERT INTO wb.api_cabinets (
    cabinet_id,
    owner_tg_id,
    display_name,
    normalized_name,
    token,
    seller_key,
    token_properties,
    binding_revision,
    capability_revision,
    content_read,
    content_write,
    client_generation,
    credential_expires_at,
    verified_at,
    status
)
VALUES
(
    'shop_000',
    :'admin_tg_id'::BIGINT,
    'Илка',
    'илка',
    'eyJhbGciOiJFUzI1NiIsImtpZCI6IjIwMjYwMzAydjEiLCJ0eXAiOiJKV1QifQ.eyJhY2MiOjMsImVudCI6MSwiZXhwIjoxODAzMzEyNjE3LCJmb3IiOiJzZWxmIiwiaWQiOiIwMWEwMzFmNi00MDQ1LTcyY2YtYjNkMC1iOTc4OTA5Y2U5M2UiLCJpaWQiOjE1MDMyMTA5LCJvaWQiOjI1MDEyMDI2MywicyI6MTgsInNpZCI6ImE3MjNmNzhkLTRkOGYtNDVlYi1hNDBiLTNkNDY4NDFkMTY4OCIsInQiOmZhbHNlLCJ1aWQiOjE1MDMyMTA5fQ.SFoWWpXmUDQDgX1ET6zZtLn1DFI_japaiGmalkCNx9crnOALma1E2dfmUr2QBqVaZFLsOzURSu-YfD9OunQRzg',
    '\x2407fb57ec4b3dc40d3e26165b88f99debeb7bd416db93dd6e4bd9c80fc192bc'::BYTEA,
    18,
    1,
    1,
    TRUE,
    TRUE,
    '\x5e8b5b3a957840a2d4d7675336589e00afa2bfaea0e58f59b134e27d4304f639'::BYTEA,
    '2027-02-23T16:10:17Z'::TIMESTAMPTZ,
    CURRENT_TIMESTAMP,
    'active'
),
(
    'shop_001',
    :'admin_tg_id'::BIGINT,
    'Фло',
    'фло',
    'eyJhbGciOiJFUzI1NiIsImtpZCI6IjIwMjYwOTAzdjEiLCJ0eXAiOiJKV1QifQ.eyJhY2MiOjMsImVudCI6MSwiZXhwIjoxODA0NzQyNzUyLCJmb3IiOiJzZWxmIiwiaWQiOiIwMWEwODczNC02MTA4LTdmMDUtOTk5Zi1kYTQzOWI2ZWY2NDIiLCJpaWQiOjE1MDMyMTA5LCJvaWQiOjI1MDA5MjQzOSwicyI6ODI1NCwic2lkIjoiNmE1ZGQ0MWEtNTY0My00YmVjLThmNzAtNzRmMDBhZDNhOTk4IiwidCI6ZmFsc2UsInVpZCI6MTUwMzIxMDl9.NCEa87ZrUslSt0GRkefzLm7P4IuOXZNEJgxOuYGdzbdwyWXXfZMJY9A-FM8BuGyxKOodut_eib9y_Sp7gIZ6iQ',
    '\x13c4f45b8a242a6569658cc9fcb0090e278d3e8cc041e65e38db4b2dc0b4f92f'::BYTEA,
    8254,
    1,
    1,
    TRUE,
    TRUE,
    '\xf8ca797b66a8ed835d3680491dcd5e71696638b0fcd585f9aeec111b035ce95a'::BYTEA,
    '2027-03-12T05:25:52Z'::TIMESTAMPTZ,
    CURRENT_TIMESTAMP,
    'active'
),
(
    'shop_002',
    :'admin_tg_id'::BIGINT,
    'Ип Абдылдаев',
    'ип абдылдаев',
    'eyJhbGciOiJFUzI1NiIsImtpZCI6IjIwMjYwOTAzdjEiLCJ0eXAiOiJKV1QifQ.eyJhY2MiOjMsImVudCI6MSwiZXhwIjoxODA0NzQyODA3LCJmb3IiOiJzZWxmIiwiaWQiOiIwMWEwODczNS0zOTk3LTc3NjgtYjEyMS02NzczZWE3ZDIzYmEiLCJpaWQiOjE1MDMyMTA5LCJvaWQiOjI1MDE4NDQ3MCwicyI6ODI0Niwic2lkIjoiMTM1OTEzMDEtYzJlZC00YmM5LTk2M2ItZDU5N2Y3ZGZjZmU5IiwidCI6ZmFsc2UsInVpZCI6MTUwMzIxMDl9.1eVl4q_jJBherAhfJMKBNR7Zyx1qmo_awBmxXAz2AHCHWxyONz8CvkU61ZnStLHK4tDEbMPVUUpqbUrp6ANY5g',
    '\x0648a563abddac1e8f30c46fef275dfb4eb2b1f1746f266f2875e96e11fa8e6b'::BYTEA,
    8246,
    1,
    1,
    TRUE,
    TRUE,
    '\x9338669a8bcea2447253e98643859e7246db02c8473c10424c87101a2f41687f'::BYTEA,
    '2027-03-12T05:26:47Z'::TIMESTAMPTZ,
    CURRENT_TIMESTAMP,
    'active'
)
ON CONFLICT (cabinet_id)
DO UPDATE SET
    owner_tg_id           = EXCLUDED.owner_tg_id,
    display_name          = EXCLUDED.display_name,
    normalized_name       = EXCLUDED.normalized_name,
    token                 = EXCLUDED.token,
    seller_key            = EXCLUDED.seller_key,
    token_properties      = EXCLUDED.token_properties,
    content_read          = EXCLUDED.content_read,
    content_write         = EXCLUDED.content_write,
    client_generation     = EXCLUDED.client_generation,
    credential_expires_at = EXCLUDED.credential_expires_at,
    verified_at           = CURRENT_TIMESTAMP,
    status                = 'active',
    updated_at            = CURRENT_TIMESTAMP;
