-- +migrate Up
ALTER TABLE users ADD COLUMN stripe_customer_id VARCHAR(32) DEFAULT "" AFTER github_token;

-- +migrate Down
ALTER TABLE users DROP COLUMN stripe_customer_id;
