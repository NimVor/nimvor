-- generated from panel/database/migrations/0019_site_parent.php — do not edit by hand
ALTER TABLE websites ADD COLUMN parent_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL;

