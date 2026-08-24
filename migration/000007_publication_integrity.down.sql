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
DROP TRIGGER publication_error_batches_reject_mutation
    ON wb.publication_error_batches;
DROP FUNCTION wb.reject_publication_error_fact_mutation;
DROP TRIGGER publication_error_cursors_protect_identity
    ON wb.publication_error_cursors;
DROP FUNCTION wb.protect_publication_error_cursor_identity;
DROP TRIGGER transfer_live_authorizations_protect_identity
    ON wb.transfer_live_authorizations;
DROP FUNCTION wb.protect_live_authorization_identity;
