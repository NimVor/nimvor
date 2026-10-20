-- generated from panel/database/migrations/0024_av_scans.php — do not edit by hand
CREATE TABLE av_scans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
    website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'manual',
    user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'running',
    quarantine INTEGER NOT NULL DEFAULT 0,
    scanned INTEGER NOT NULL DEFAULT 0,
    infected INTEGER NOT NULL DEFAULT 0,
    findings TEXT NOT NULL DEFAULT '[]',
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    finished_at TEXT NULL
);

CREATE INDEX av_scans_website ON av_scans (website_id, id);

CREATE INDEX av_scans_status ON av_scans (server_id, status);

