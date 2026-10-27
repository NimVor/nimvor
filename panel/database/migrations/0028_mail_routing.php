<?php

declare(strict_types=1);

// Mail routing per domain: local (mailboxes here), backup (queue for the
// primary MX) or remote (another server receives the domain's mail).

return [
    "ALTER TABLE mail_domains ADD COLUMN routing TEXT NOT NULL DEFAULT 'local'",
];
