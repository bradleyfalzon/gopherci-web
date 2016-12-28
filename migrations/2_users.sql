-- +migrate Up
CREATE TABLE `users` (
	id INT UNSIGNED AUTO_INCREMENT,
	github_id INT UNSIGNED NULL DEFAULT NULL,
	github_token VARCHAR(128) NULL DEFAULT NULL,
	created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	PRIMARY KEY (`id`),
    UNIQUE KEY `github_id` (`github_id`)
) ENGINE=innodb;

-- +migrate Down
DROP TABLE `users`;
