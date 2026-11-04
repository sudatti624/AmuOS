-- +goose Up
ALTER TABLE users ADD COLUMN authorization_version INTEGER NOT NULL DEFAULT 1 CHECK(authorization_version>=1);
