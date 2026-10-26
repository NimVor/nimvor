-- Go panel only (no PHP counterpart): sessions are sealed client-held
-- cookies, so signing out records the session id here; a copied cookie is
-- refused afterwards. Rows are pruned after the absolute session lifetime.
CREATE TABLE IF NOT EXISTS revoked_sessions (
    sid TEXT PRIMARY KEY,
    revoked_at INTEGER NOT NULL
);
