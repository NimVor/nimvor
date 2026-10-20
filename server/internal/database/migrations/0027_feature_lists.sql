-- generated from panel/database/migrations/0027_feature_lists.php — do not edit by hand
CREATE TABLE feature_lists (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE feature_list_disabled (
    list_id INTEGER NOT NULL REFERENCES feature_lists(id) ON DELETE CASCADE,
    feature TEXT NOT NULL,
    PRIMARY KEY (list_id, feature)
);

ALTER TABLE plans ADD COLUMN feature_list_id INTEGER NULL REFERENCES feature_lists(id) ON DELETE SET NULL;

