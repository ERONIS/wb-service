CREATE FUNCTION wb.protect_publication_plan_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.plan_digest,
        NEW.target_set_root,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.plan_digest,
        OLD.target_set_root,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'publication plan identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_plans_protect_identity
BEFORE UPDATE ON wb.publication_plans
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_plan_identity();


CREATE FUNCTION wb.protect_publication_observation_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'publication observation is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_observations_protect_identity
BEFORE UPDATE ON wb.publication_observations
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_observation_identity();


CREATE FUNCTION wb.protect_live_authorization_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.plan_id,
        NEW.plan_digest,
        NEW.target_set_root,
        NEW.trusted_actor_id,
        NEW.trusted_actor_digest,
        NEW.trusted_actor_name,
        NEW.requested_at,
        NEW.expires_at,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.plan_id,
        OLD.plan_digest,
        OLD.target_set_root,
        OLD.trusted_actor_id,
        OLD.trusted_actor_digest,
        OLD.trusted_actor_name,
        OLD.requested_at,
        OLD.expires_at,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'live authorization identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER transfer_live_authorizations_protect_identity
BEFORE UPDATE ON wb.transfer_live_authorizations
FOR EACH ROW EXECUTE FUNCTION wb.protect_live_authorization_identity();


CREATE FUNCTION wb.reject_live_authorization_command_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'live authorization command is immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER transfer_live_authorization_commands_reject_mutation
BEFORE UPDATE OR DELETE ON wb.transfer_live_authorization_commands
FOR EACH ROW EXECUTE FUNCTION wb.reject_live_authorization_command_mutation();


CREATE FUNCTION wb.protect_publication_error_cursor_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.cabinet_id IS DISTINCT FROM OLD.cabinet_id
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'publication error cursor identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_error_cursors_protect_identity
BEFORE UPDATE ON wb.publication_error_cursors
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_error_cursor_identity();


CREATE FUNCTION wb.reject_publication_error_fact_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'publication error evidence is immutable'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER publication_error_batches_reject_mutation
BEFORE UPDATE OR DELETE ON wb.publication_error_batches
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_error_fact_mutation();

CREATE FUNCTION wb.protect_publication_action_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.plan_id,
        NEW.target_id,
        NEW.action_key,
        NEW.kind,
        NEW.request_digest,
        NEW.request_payload,
        NEW.member_set_digest,
        NEW.media_link_set_root,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.plan_id,
        OLD.target_id,
        OLD.action_key,
        OLD.kind,
        OLD.request_digest,
        OLD.request_payload,
        OLD.member_set_digest,
        OLD.media_link_set_root,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'publication action identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_actions_protect_identity
BEFORE UPDATE ON wb.publication_actions
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_action_identity();


CREATE FUNCTION wb.protect_publication_action_member_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.action_id,
        NEW.target_id,
        NEW.group_target_id,
        NEW.transfer_item_target_id,
        NEW.request_member_index,
        NEW.vendor_code,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.action_id,
        OLD.target_id,
        OLD.group_target_id,
        OLD.transfer_item_target_id,
        OLD.request_member_index,
        OLD.vendor_code,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'publication action member identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_action_members_protect_identity
BEFORE UPDATE ON wb.publication_action_members
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_action_member_identity();


CREATE FUNCTION wb.protect_publication_attempt_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.transfer_id,
        NEW.action_id,
        NEW.authorization_id,
        NEW.recheck_observation_id,
        NEW.baseline_cabinet_id,
        NEW.baseline_cursor_revision,
        NEW.baseline_cursor_updated_at,
        NEW.baseline_cursor_batch_uuid,
        NEW.baseline_captured_at,
        NEW.request_digest,
        NEW.request_payload,
        NEW.attribution_id,
        NEW.started_at
    ) IS DISTINCT FROM ROW(
        OLD.transfer_id,
        OLD.action_id,
        OLD.authorization_id,
        OLD.recheck_observation_id,
        OLD.baseline_cabinet_id,
        OLD.baseline_cursor_revision,
        OLD.baseline_cursor_updated_at,
        OLD.baseline_cursor_batch_uuid,
        OLD.baseline_captured_at,
        OLD.request_digest,
        OLD.request_payload,
        OLD.attribution_id,
        OLD.started_at
    ) THEN
        RAISE EXCEPTION 'publication attempt identity is immutable'
            USING ERRCODE = '55000';
    END IF;

    IF OLD.finished_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'finished publication attempt is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_attempts_protect_identity
BEFORE UPDATE ON wb.publication_attempts
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_attempt_identity();


CREATE FUNCTION wb.protect_publication_attribution_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'publication attribution is immutable'
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER publication_attributions_protect_identity
BEFORE UPDATE ON wb.publication_attributions
FOR EACH ROW EXECUTE FUNCTION wb.protect_publication_attribution_identity();


CREATE FUNCTION wb.reject_publication_fact_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'publication facts cannot be deleted'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER publication_plans_reject_delete
BEFORE DELETE ON wb.publication_plans
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_observations_reject_delete
BEFORE DELETE ON wb.publication_observations
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER transfer_live_authorizations_reject_delete
BEFORE DELETE ON wb.transfer_live_authorizations
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_actions_reject_delete
BEFORE DELETE ON wb.publication_actions
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_action_members_reject_delete
BEFORE DELETE ON wb.publication_action_members
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_attempts_reject_delete
BEFORE DELETE ON wb.publication_attempts
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_attributions_reject_delete
BEFORE DELETE ON wb.publication_attributions
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_error_correlations_reject_mutation
BEFORE UPDATE OR DELETE ON wb.publication_error_correlations
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();

CREATE TRIGGER publication_manual_resolutions_reject_mutation
BEFORE UPDATE OR DELETE ON wb.publication_manual_resolutions
FOR EACH ROW EXECUTE FUNCTION wb.reject_publication_fact_delete();


