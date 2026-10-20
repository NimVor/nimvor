-- generated from panel/database/migrations/0047_setup_wizard.php — do not edit by hand
INSERT OR IGNORE INTO settings (key, value, updated_at)
SELECT k, 'true', strftime('%Y-%m-%dT%H:%M:%S+00:00', 'now')
FROM (SELECT 'setup_wizard_seen' AS k UNION ALL SELECT 'setup_wizard_done')
WHERE EXISTS (SELECT 1 FROM websites);

