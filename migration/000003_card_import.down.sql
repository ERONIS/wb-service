DROP TRIGGER IF EXISTS card_batch_items_reject_mutation ON wb.card_batch_items;
DROP TRIGGER IF EXISTS card_batch_items_validate_insert ON wb.card_batch_items;
DROP TRIGGER IF EXISTS card_batches_reject_mutation ON wb.card_batches;
DROP FUNCTION IF EXISTS wb.validate_card_batch_item_insert;
DROP FUNCTION IF EXISTS wb.reject_frozen_card_batch_mutation;
DROP TABLE IF EXISTS wb.card_batch_items;
DROP TABLE IF EXISTS wb.card_batches;
