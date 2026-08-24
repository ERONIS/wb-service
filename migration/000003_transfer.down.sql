DROP TRIGGER transfer_targets_reject_activated_insert ON wb.transfer_targets;
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
DROP TRIGGER transfer_items_reject_activated_mutation ON wb.transfer_items;
DROP TRIGGER transfer_groups_reject_activated_mutation ON wb.transfer_groups;
DROP FUNCTION wb.reject_activated_transfer_derived_mutation;
DROP TABLE wb.transfer_item_targets;
DROP TABLE wb.transfer_group_targets;
DROP TABLE wb.transfer_items;
DROP TABLE wb.transfer_groups;
DROP TABLE wb.transfer_targets;
DROP FUNCTION wb.reject_transfer_target_mutation;
DROP TABLE wb.transfers;
DROP FUNCTION wb.protect_transfer_frozen_fields;
