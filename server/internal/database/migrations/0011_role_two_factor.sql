-- generated from panel/database/migrations/0011_role_two_factor.php — do not edit by hand
ALTER TABLE roles ADD COLUMN require_two_factor INTEGER NOT NULL DEFAULT 0;

