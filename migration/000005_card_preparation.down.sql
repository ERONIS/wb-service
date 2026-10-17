DROP TRIGGER IF EXISTS card_preparation_artifacts_reject_mutation
    ON wb.card_preparation_artifacts;
DROP FUNCTION IF EXISTS wb.reject_card_preparation_artifact_mutation;
DROP TABLE IF EXISTS wb.card_preparation_artifacts;

