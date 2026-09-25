-- Prepared groups are published in immutable waves. Recording ownership on the
-- projection makes every group belong to exactly one publication plan.
ALTER TABLE wb.transfer_group_targets
ADD COLUMN publication_plan_id BIGINT;

-- Failed preparation groups are terminal and do not require a publication
-- plan. Normalize transfers that were between preparation and planning during
-- deployment of this migration.
UPDATE wb.transfer_group_targets
SET publication_status = 'skipped',
    media_status = 'skipped'
WHERE preparation_status IN ('rejected', 'unresolved')
  AND publication_status = 'not_started';

-- Before this migration a transfer had one effective plan, so it is the only
-- possible owner for already-planned projections.
UPDATE wb.transfer_group_targets AS group_target
SET publication_plan_id = (
    SELECT plan.id
    FROM wb.publication_plans AS plan
    WHERE plan.transfer_id = group_target.transfer_id
      AND plan.state <> 'superseded'
    ORDER BY plan.id DESC
    LIMIT 1
)
WHERE group_target.publication_status <> 'not_started';

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

DROP INDEX wb.transfer_live_authorizations_one_open_idx;

CREATE UNIQUE INDEX transfer_live_authorizations_one_open_plan_idx
ON wb.transfer_live_authorizations (transfer_id, plan_id)
WHERE state IN ('requested', 'authorized');
