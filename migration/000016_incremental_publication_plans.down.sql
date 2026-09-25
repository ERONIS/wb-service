DROP INDEX wb.transfer_live_authorizations_one_open_plan_idx;

-- The previous schema allows only one open authorization for a transfer.
-- Close older waves before restoring that constraint.
WITH ranked_open AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY transfer_id
               ORDER BY id DESC
           ) AS position
    FROM wb.transfer_live_authorizations
    WHERE state IN ('requested', 'authorized')
)
UPDATE wb.transfer_live_authorizations AS live_auth
SET state = 'superseded',
    closed_at = CURRENT_TIMESTAMP,
    safe_reason_code = 'INCREMENTAL_PLAN_MIGRATION_ROLLBACK',
    revision = live_auth.revision + 1,
    updated_at = CURRENT_TIMESTAMP
FROM ranked_open
WHERE ranked_open.id = live_auth.id
  AND ranked_open.position > 1;

CREATE UNIQUE INDEX transfer_live_authorizations_one_open_idx
ON wb.transfer_live_authorizations (transfer_id)
WHERE state IN ('requested', 'authorized');

DROP INDEX wb.transfer_group_targets_publication_plan_idx;

DROP INDEX wb.transfer_group_targets_unplanned_prepared_idx;

ALTER TABLE wb.transfer_group_targets
DROP CONSTRAINT transfer_group_targets_publication_plan_state_check;

ALTER TABLE wb.transfer_group_targets
DROP CONSTRAINT transfer_group_targets_publication_plan_fk;

ALTER TABLE wb.transfer_group_targets
DROP COLUMN publication_plan_id;
