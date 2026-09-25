DROP TRIGGER IF EXISTS publication_manual_resolutions_reject_mutation
    ON wb.publication_manual_resolutions;
DROP TRIGGER IF EXISTS publication_error_correlations_reject_mutation
    ON wb.publication_error_correlations;
DROP TRIGGER IF EXISTS publication_product_retries_reject_delete
    ON wb.publication_product_retries;
DROP TRIGGER IF EXISTS publication_product_retries_protect
    ON wb.publication_product_retries;
DROP TRIGGER IF EXISTS publication_attributions_reject_delete
    ON wb.publication_attributions;
DROP TRIGGER IF EXISTS publication_attempts_reject_delete
    ON wb.publication_attempts;
DROP TRIGGER IF EXISTS publication_action_members_reject_delete
    ON wb.publication_action_members;
DROP TRIGGER IF EXISTS publication_actions_reject_delete
    ON wb.publication_actions;
DROP TRIGGER IF EXISTS publication_plans_reject_delete
    ON wb.publication_plans;
DROP TRIGGER IF EXISTS publication_observations_reject_delete
    ON wb.publication_observations;
DROP TRIGGER IF EXISTS transfer_live_authorizations_reject_delete
    ON wb.transfer_live_authorizations;
DROP FUNCTION IF EXISTS wb.reject_publication_fact_delete;
DROP FUNCTION IF EXISTS wb.protect_publication_product_retry;

DROP TRIGGER IF EXISTS publication_attributions_protect_identity
    ON wb.publication_attributions;
DROP FUNCTION IF EXISTS wb.protect_publication_attribution_identity;

DROP TRIGGER IF EXISTS publication_attempts_protect_identity
    ON wb.publication_attempts;
DROP FUNCTION IF EXISTS wb.protect_publication_attempt_identity;

DROP TRIGGER IF EXISTS publication_action_members_protect_identity
    ON wb.publication_action_members;
DROP FUNCTION IF EXISTS wb.protect_publication_action_member_identity;

DROP TRIGGER IF EXISTS publication_actions_protect_identity
    ON wb.publication_actions;
DROP FUNCTION IF EXISTS wb.protect_publication_action_identity;

DROP TRIGGER IF EXISTS publication_plans_protect_identity
    ON wb.publication_plans;
DROP FUNCTION IF EXISTS wb.protect_publication_plan_identity;

DROP TRIGGER IF EXISTS publication_observations_protect_identity
    ON wb.publication_observations;
DROP FUNCTION IF EXISTS wb.protect_publication_observation_identity;

DROP TRIGGER IF EXISTS transfer_live_authorization_commands_reject_mutation
    ON wb.transfer_live_authorization_commands;
DROP FUNCTION IF EXISTS wb.reject_live_authorization_command_mutation;

DROP TRIGGER IF EXISTS publication_error_batches_reject_mutation
    ON wb.publication_error_batches;
DROP FUNCTION IF EXISTS wb.reject_publication_error_fact_mutation;

DROP TRIGGER IF EXISTS publication_error_cursors_protect_identity
    ON wb.publication_error_cursors;
DROP FUNCTION IF EXISTS wb.protect_publication_error_cursor_identity;

DROP TRIGGER IF EXISTS transfer_live_authorizations_protect_identity
    ON wb.transfer_live_authorizations;
DROP FUNCTION IF EXISTS wb.protect_live_authorization_identity;

DROP TABLE IF EXISTS wb.publication_product_retries;
DROP TABLE IF EXISTS wb.publication_media_visibility;

ALTER TABLE wb.transfer_item_targets
    DROP CONSTRAINT IF EXISTS transfer_item_targets_source_action_fk;

ALTER TABLE wb.publication_attempts
    DROP CONSTRAINT IF EXISTS publication_attempts_attribution_fk;

DROP INDEX IF EXISTS wb.transfer_group_targets_publication_plan_idx;
DROP INDEX IF EXISTS wb.transfer_group_targets_unplanned_prepared_idx;

ALTER TABLE wb.transfer_group_targets
    DROP CONSTRAINT IF EXISTS transfer_group_targets_publication_plan_state_check;

ALTER TABLE wb.transfer_group_targets
    DROP CONSTRAINT IF EXISTS transfer_group_targets_publication_plan_fk;

ALTER TABLE wb.transfer_group_targets
    DROP COLUMN IF EXISTS publication_plan_id;

DROP TABLE IF EXISTS wb.publication_manual_resolutions;
DROP TABLE IF EXISTS wb.publication_attributions;
DROP TABLE IF EXISTS wb.publication_error_correlations;
DROP TABLE IF EXISTS wb.publication_attempts;
DROP TABLE IF EXISTS wb.publication_action_members;
DROP TABLE IF EXISTS wb.publication_error_batches;
DROP TABLE IF EXISTS wb.publication_error_cursors;

ALTER TABLE wb.publication_actions
    DROP CONSTRAINT IF EXISTS publication_actions_live_authorization_fk;

DROP TABLE IF EXISTS wb.transfer_live_authorization_commands;
DROP TABLE IF EXISTS wb.transfer_live_authorizations;
DROP TABLE IF EXISTS wb.product_identities;
DROP TABLE IF EXISTS wb.publication_actions;
DROP TABLE IF EXISTS wb.publication_plans;
DROP TABLE IF EXISTS wb.publication_observations;
