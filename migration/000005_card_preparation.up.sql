CREATE TABLE wb.card_preparation_artifacts (
    group_target_id         BIGINT NOT NULL,
    transfer_id             BIGINT NOT NULL,
    subject_id              BIGINT NOT NULL
                            CHECK (subject_id > 0),
    request_payload         BYTEA NOT NULL
                            CHECK (
                                octet_length(request_payload) > 0
                                AND octet_length(request_payload) <= 10000000
                            ),
    metadata_snapshot       JSONB NOT NULL
                            CHECK (jsonb_typeof(metadata_snapshot) = 'object'),
    limits_free             BIGINT NOT NULL
                            CHECK (limits_free >= 0),
    limits_paid             BIGINT NOT NULL
                            CHECK (limits_paid >= 0),
    limits_observed_at      TIMESTAMPTZ NOT NULL,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (transfer_id, group_target_id),

    FOREIGN KEY (transfer_id, group_target_id)
        REFERENCES wb.transfer_group_targets (transfer_id, id)
);

CREATE FUNCTION wb.reject_card_preparation_artifact_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'immutable card preparation artifact cannot be changed'
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER card_preparation_artifacts_reject_mutation
BEFORE UPDATE OR DELETE ON wb.card_preparation_artifacts
FOR EACH ROW EXECUTE FUNCTION wb.reject_card_preparation_artifact_mutation();
