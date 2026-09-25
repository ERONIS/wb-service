ALTER TABLE wb.users
DROP CONSTRAINT users_role_check;

ALTER TABLE wb.users
ADD CONSTRAINT users_role_check
CHECK (role IN ('user', 'partner', 'admin'));
