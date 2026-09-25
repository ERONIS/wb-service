ALTER TABLE wb.api_cabinets
ADD COLUMN token_properties BIGINT NOT NULL DEFAULT 0
CHECK (token_properties >= 0);
