DROP TABLE wb.product_identities;
ALTER TABLE wb.publication_actions
    DROP CONSTRAINT publication_actions_live_authorization_fk;
DROP TABLE wb.transfer_live_authorization_commands;
DROP TABLE wb.transfer_live_authorizations;
DROP TABLE wb.publication_actions;
DROP TABLE wb.publication_plans;
DROP TABLE wb.publication_observations;
