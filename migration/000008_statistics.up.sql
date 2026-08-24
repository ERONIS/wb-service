CREATE VIEW wb.statistics_transfer_facts AS
SELECT
    transfer.id AS transfer_id,
    transfer.batch_id,
    transfer.phase,
    transfer.outcome,
    transfer.attention_code,
    transfer.cohort_name,
    transfer.items_count,
    transfer.groups_count,
    transfer.targets_count,
    transfer.item_targets_count,
    transfer.group_targets_count,
    transfer.created_at,
    transfer.started_at,
    transfer.finished_at
FROM wb.transfers AS transfer;


CREATE VIEW wb.statistics_item_facts AS
SELECT
    item_target.transfer_id,
    item_target.id AS item_target_id,
    item_target.group_target_id,
    item_target.transfer_item_id,
    target.id AS target_id,
    target.position AS target_position,
    target.cabinet_id,
    item.position AS item_position,
    item.vendor_code,
    item_target.state,
    item_target.outcome_class,
    item_target.outcome_code,
    item_target.nm_id,
    item_target.source_action_id,
    item_target.attention_closed_at,
    item_target.created_at,
    item_target.started_at,
    item_target.finished_at
FROM wb.transfer_item_targets AS item_target
JOIN wb.transfer_group_targets AS group_target
  ON group_target.transfer_id = item_target.transfer_id
 AND group_target.id = item_target.group_target_id
JOIN wb.transfer_targets AS target
  ON target.transfer_id = group_target.transfer_id
 AND target.id = group_target.target_id
JOIN wb.transfer_items AS item
  ON item.transfer_id = item_target.transfer_id
 AND item.id = item_target.transfer_item_id;


CREATE VIEW wb.statistics_action_facts AS
SELECT
    action.transfer_id,
    action.id AS action_id,
    action.plan_id,
    action.target_id,
    target.position AS target_position,
    target.cabinet_id,
    action.kind,
    action.state,
    action.outcome_class,
    action.outcome_code,
    action.authorization_id,
    action.created_at,
    action.started_at,
    action.finished_at
FROM wb.publication_actions AS action
JOIN wb.transfer_targets AS target
  ON target.transfer_id = action.transfer_id
 AND target.id = action.target_id;


CREATE VIEW wb.statistics_attempt_facts AS
SELECT
    attempt.transfer_id,
    attempt.id AS attempt_id,
    attempt.action_id,
    attempt.authorization_id,
    attempt.recheck_observation_id,
    attempt.attribution_id,
    action.plan_id,
    action.target_id,
    target.position AS target_position,
    target.cabinet_id,
    action.kind AS action_kind,
    attempt.delivery_state,
    attempt.http_status,
    attempt.response_disposition,
    attempt.classifier_version,
    attempt.safe_error_code,
    attempt.unmatched_count,
    attempt.baseline_cabinet_id AS error_baseline_cabinet_id,
    attempt.baseline_cursor_revision AS error_cursor_revision,
    attempt.baseline_cursor_updated_at AS error_cursor_updated_at,
    attempt.baseline_cursor_batch_uuid AS error_cursor_batch_uuid,
    attempt.baseline_captured_at AS error_baseline_captured_at,
    attempt.started_at,
    attempt.finished_at
FROM wb.publication_attempts AS attempt
JOIN wb.publication_actions AS action
  ON action.transfer_id = attempt.transfer_id
 AND action.id = attempt.action_id
JOIN wb.transfer_targets AS target
  ON target.transfer_id = action.transfer_id
 AND target.id = action.target_id;


CREATE VIEW wb.statistics_authorization_facts AS
SELECT
    live_auth.transfer_id,
    live_auth.id AS authorization_id,
    live_auth.plan_id,
    live_auth.plan_digest,
    live_auth.target_set_root,
    live_auth.revision,
    live_auth.state,
    live_auth.trusted_actor_digest,
    live_auth.requested_at,
    live_auth.approved_at,
    live_auth.expires_at,
    live_auth.revoked_at,
    live_auth.closed_at,
    live_auth.safe_reason_code,
    live_auth.created_at,
    live_auth.updated_at
FROM wb.transfer_live_authorizations AS live_auth;


CREATE VIEW wb.statistics_error_batch_facts AS
SELECT
    batch.id AS error_batch_id,
    batch.cabinet_id,
    batch.batch_uuid,
    batch.batch_updated_at,
    batch.source_digest,
    cardinality(batch.vendor_codes) AS vendor_codes_count,
    cardinality(batch.rejected_vendor_codes) AS rejected_vendor_codes_count,
    batch.error_codes,
    batch.observed_at,
    batch.created_at
FROM wb.publication_error_batches AS batch;
