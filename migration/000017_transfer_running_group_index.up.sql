CREATE INDEX transfer_group_targets_running_idx
ON wb.transfer_group_targets (transfer_id)
WHERE overall_outcome = 'running';
