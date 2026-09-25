DROP TRIGGER IF EXISTS card_preparations_protect_identity ON wb.card_preparations;
DROP FUNCTION IF EXISTS wb.protect_card_preparation_identity;
DROP TRIGGER IF EXISTS card_preparation_groups_protect_identity
    ON wb.card_preparation_groups;
DROP FUNCTION IF EXISTS wb.protect_card_preparation_work_identity;
DROP TRIGGER IF EXISTS card_preparation_items_reject_mutation
    ON wb.card_preparation_items;
DROP TRIGGER IF EXISTS card_preparation_artifacts_reject_mutation
    ON wb.card_preparation_artifacts;
DROP FUNCTION IF EXISTS wb.reject_card_preparation_artifact_mutation;
DROP TABLE IF EXISTS wb.card_preparation_items;
DROP TABLE IF EXISTS wb.card_preparation_artifacts;
DROP TABLE IF EXISTS wb.card_preparation_groups;
DROP TABLE IF EXISTS wb.card_preparations;
