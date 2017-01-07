-- +migrate Up
ALTER TABLE users ADD COLUMN email VARCHAR(128) DEFAULT "" AFTER id;

-- +migrate Down
ALTER TABLE users DROP COLUMN email;
