DROP VIEW wb.statistics_error_batch_facts;
DROP VIEW wb.statistics_authorization_facts;
DROP VIEW wb.statistics_attempt_facts;
DROP VIEW wb.statistics_action_facts;
DROP VIEW wb.statistics_item_facts;
DROP VIEW wb.statistics_transfer_facts;
DROP TRIGGER publication_manual_resolutions_reject_mutation
    ON wb.publication_manual_resolutions;
DROP TRIGGER publication_error_correlations_reject_mutation
    ON wb.publication_error_correlations;
DROP TRIGGER publication_attributions_reject_delete
    ON wb.publication_attributions;
DROP TRIGGER publication_attempts_reject_delete ON wb.publication_attempts;
DROP TRIGGER publication_action_members_reject_delete
    ON wb.publication_action_members;
DROP TRIGGER publication_actions_reject_delete ON wb.publication_actions;
DROP TRIGGER publication_plans_reject_delete ON wb.publication_plans;
DROP TRIGGER publication_observations_reject_delete
    ON wb.publication_observations;
DROP TRIGGER transfer_live_authorizations_reject_delete
    ON wb.transfer_live_authorizations;
DROP FUNCTION wb.reject_publication_fact_delete;
DROP TRIGGER publication_attributions_protect_identity
    ON wb.publication_attributions;
DROP FUNCTION wb.protect_publication_attribution_identity;
DROP TRIGGER publication_attempts_protect_identity ON wb.publication_attempts;
DROP FUNCTION wb.protect_publication_attempt_identity;
DROP TRIGGER publication_action_members_protect_identity
    ON wb.publication_action_members;
DROP FUNCTION wb.protect_publication_action_member_identity;
DROP TRIGGER publication_actions_protect_identity ON wb.publication_actions;
DROP FUNCTION wb.protect_publication_action_identity;
DROP TRIGGER publication_plans_protect_identity ON wb.publication_plans;
DROP FUNCTION wb.protect_publication_plan_identity;
DROP TRIGGER publication_observations_protect_identity
    ON wb.publication_observations;
DROP FUNCTION wb.protect_publication_observation_identity;
DROP TRIGGER transfer_live_authorization_commands_reject_mutation
    ON wb.transfer_live_authorization_commands;
DROP FUNCTION wb.reject_live_authorization_command_mutation;
DROP TRIGGER publication_error_baselines_reject_mutation
    ON wb.publication_error_baselines;
DROP TRIGGER publication_error_batches_reject_mutation
    ON wb.publication_error_batches;
DROP FUNCTION wb.reject_publication_error_fact_mutation;
DROP TRIGGER publication_error_cursors_protect_identity
    ON wb.publication_error_cursors;
DROP FUNCTION wb.protect_publication_error_cursor_identity;
DROP TRIGGER transfer_live_authorizations_protect_identity
    ON wb.transfer_live_authorizations;
DROP FUNCTION wb.protect_live_authorization_identity;
ALTER TABLE wb.transfer_item_targets
    DROP CONSTRAINT transfer_item_targets_source_action_fk;
ALTER TABLE wb.publication_attempts
    DROP CONSTRAINT publication_attempts_attribution_fk;
DROP TABLE wb.publication_manual_resolutions;
DROP TABLE wb.publication_attributions;
DROP TABLE wb.publication_error_correlations;
DROP TABLE wb.publication_attempts;
DROP TABLE wb.publication_error_baselines;
DROP TABLE wb.publication_error_batches;
DROP TABLE wb.publication_error_cursors;
DROP TABLE wb.publication_action_members;
DROP TABLE wb.product_identities;
ALTER TABLE wb.publication_actions
    DROP CONSTRAINT publication_actions_live_authorization_fk;
DROP TABLE wb.transfer_live_authorization_commands;
DROP TABLE wb.transfer_live_authorizations;
DROP TABLE wb.publication_actions;
DROP TABLE wb.publication_plans;
DROP TABLE wb.publication_observations;
DROP TRIGGER transfer_targets_reject_activated_insert ON wb.transfer_targets;
DROP TRIGGER card_preparations_protect_identity ON wb.card_preparations;
DROP FUNCTION wb.protect_card_preparation_identity;
DROP TRIGGER card_preparation_groups_protect_identity
    ON wb.card_preparation_groups;
DROP FUNCTION wb.protect_card_preparation_work_identity;
DROP TRIGGER card_preparation_items_reject_mutation
    ON wb.card_preparation_items;
DROP TRIGGER card_preparation_payloads_reject_mutation
    ON wb.card_preparation_payloads;
DROP TRIGGER card_metadata_snapshots_reject_mutation
    ON wb.card_metadata_snapshots;
DROP FUNCTION wb.reject_card_preparation_artifact_mutation;
DROP TABLE wb.card_preparation_items;
DROP TABLE wb.card_preparation_payloads;
DROP TABLE wb.card_metadata_snapshots;
DROP TABLE wb.card_preparation_groups;
DROP TABLE wb.card_preparations;
DROP TRIGGER transfer_item_targets_reject_activated_membership
    ON wb.transfer_item_targets;
DROP TRIGGER transfer_group_targets_reject_activated_membership
    ON wb.transfer_group_targets;
DROP TRIGGER transfer_item_targets_protect_identity
    ON wb.transfer_item_targets;
DROP FUNCTION wb.protect_transfer_item_target_identity;
DROP TRIGGER transfer_group_targets_protect_identity
    ON wb.transfer_group_targets;
DROP FUNCTION wb.protect_transfer_group_target_identity;
DROP TRIGGER transfer_group_members_reject_activated_mutation
    ON wb.transfer_group_members;
DROP TRIGGER transfer_items_reject_activated_mutation ON wb.transfer_items;
DROP TRIGGER transfer_groups_reject_activated_mutation ON wb.transfer_groups;
DROP FUNCTION wb.reject_activated_transfer_derived_mutation;
DROP TABLE wb.transfer_item_targets;
DROP TABLE wb.transfer_group_targets;
DROP TABLE wb.transfer_group_members;
DROP TABLE wb.transfer_items;
DROP TABLE wb.transfer_groups;
DROP TABLE wb.transfer_targets;
DROP FUNCTION wb.reject_transfer_target_mutation;
DROP TABLE wb.transfers;
DROP FUNCTION wb.protect_transfer_frozen_fields;
DROP TABLE wb.card_batch_items;
ALTER TABLE wb.card_import_sessions
    DROP CONSTRAINT card_import_sessions_finalized_batch_fk;
DROP TABLE wb.card_batches;
DROP FUNCTION wb.validate_card_batch_item_insert;
DROP FUNCTION wb.reject_frozen_card_batch_mutation;
DROP TABLE wb.card_import_items;
DROP TABLE wb.card_import_issues;
DROP TABLE wb.card_import_file_blobs;
DROP TABLE wb.card_import_files;
DROP TABLE wb.card_import_sessions;
DROP TABLE wb.cabinet_identity_bindings;
DROP TABLE wb.users;
DROP SCHEMA wb;
