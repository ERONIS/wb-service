DROP TRIGGER card_preparations_protect_identity ON wb.card_preparations;
DROP FUNCTION wb.protect_card_preparation_identity;
DROP TRIGGER card_preparation_groups_protect_identity
    ON wb.card_preparation_groups;
DROP FUNCTION wb.protect_card_preparation_work_identity;
DROP TRIGGER card_preparation_items_reject_mutation
    ON wb.card_preparation_items;
DROP TRIGGER card_preparation_artifacts_reject_mutation
    ON wb.card_preparation_artifacts;
DROP FUNCTION wb.reject_card_preparation_artifact_mutation;
DROP TABLE wb.card_preparation_items;
DROP TABLE wb.card_preparation_artifacts;
DROP TABLE wb.card_preparation_groups;
DROP TABLE wb.card_preparations;
