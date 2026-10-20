-- generated from panel/database/migrations/0054_feature_defaults.php — do not edit by hand
CREATE TABLE feature_list_enabled (
    list_id INTEGER NOT NULL REFERENCES feature_lists(id) ON DELETE CASCADE,
    feature TEXT NOT NULL,
    PRIMARY KEY (list_id, feature)
);

