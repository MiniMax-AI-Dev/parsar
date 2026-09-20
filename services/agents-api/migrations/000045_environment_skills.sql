-- +goose Up
ALTER TABLE environment_templates
 ADD COLUMN skills jsonb NOT NULL DEFAULT '[]'::jsonb,
 ADD COLUMN skill_contents bytea;

-- +goose Down
ALTER TABLE environment_templates DROP COLUMN skill_contents, DROP COLUMN skills;
