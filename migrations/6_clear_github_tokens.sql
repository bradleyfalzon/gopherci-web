-- +migrate Up
UPDATE users SET github_token = NULL;

-- +migrate Down
-- +migrate StatementBegin
-- +migrate StatementEnd
