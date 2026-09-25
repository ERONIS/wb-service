CREATE INDEX transfer_group_targets_preparation_pending_idx
ON wb.transfer_group_targets (transfer_id)
WHERE preparation_status NOT IN ('succeeded', 'rejected', 'unresolved');

CREATE INDEX publication_actions_plan_unfinished_idx
ON wb.publication_actions (transfer_id, plan_id)
WHERE state NOT IN ('terminal', 'superseded');
