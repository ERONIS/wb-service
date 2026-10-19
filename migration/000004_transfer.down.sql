DROP TRIGGER IF EXISTS transfer_targets_reject_activated_insert ON wb.transfer_targets;
DROP TRIGGER IF EXISTS transfer_item_targets_reject_activated_membership
    ON wb.transfer_item_targets;
DROP TRIGGER IF EXISTS transfer_group_targets_reject_activated_membership
    ON wb.transfer_group_targets;
DROP TRIGGER IF EXISTS transfer_item_targets_protect_identity
    ON wb.transfer_item_targets;
DROP FUNCTION IF EXISTS wb.protect_transfer_item_target_identity;
DROP TRIGGER IF EXISTS transfer_group_targets_protect_identity
    ON wb.transfer_group_targets;
DROP FUNCTION IF EXISTS wb.protect_transfer_group_target_identity;
DROP TRIGGER IF EXISTS transfer_items_reject_activated_mutation ON wb.transfer_items;
DROP TRIGGER IF EXISTS transfer_groups_reject_activated_mutation ON wb.transfer_groups;
DROP FUNCTION IF EXISTS wb.reject_activated_transfer_derived_mutation;
DROP TABLE IF EXISTS wb.transfer_item_targets;
DROP TABLE IF EXISTS wb.transfer_group_targets;
DROP TABLE IF EXISTS wb.transfer_items;
DROP TABLE IF EXISTS wb.transfer_groups;
DROP TABLE IF EXISTS wb.transfer_targets;
DROP FUNCTION IF EXISTS wb.reject_transfer_target_mutation;
DROP TABLE IF EXISTS wb.transfers;
DROP FUNCTION IF EXISTS wb.protect_transfer_frozen_fields;
