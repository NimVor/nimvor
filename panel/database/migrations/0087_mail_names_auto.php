<?php

declare(strict_types=1);

// Mail names (mail.<domain>, webmail.<domain>) turn on by themselves once the domain's DNS points at this server
// (MailNamesAuto). Per mail domain: where it stands ('pending', 'enabling', 'done', 'gave_up', or 'off' when an
// administrator turned the names off, which is respected), how many certificate attempts failed, when the
// automatic window began, when to look again, and a line for the Mail names card. Domains that already exist get
// a fresh window starting now, so the ones without mail names are looked at too.

return [
    "ALTER TABLE mail_domains ADD COLUMN names_auto TEXT NOT NULL DEFAULT 'pending'",
    "ALTER TABLE mail_domains ADD COLUMN names_auto_tries INTEGER NOT NULL DEFAULT 0",
    "ALTER TABLE mail_domains ADD COLUMN names_auto_since TEXT",
    "ALTER TABLE mail_domains ADD COLUMN names_auto_next TEXT",
    "ALTER TABLE mail_domains ADD COLUMN names_auto_note TEXT NOT NULL DEFAULT ''",
    "UPDATE mail_domains SET names_auto_since = strftime('%Y-%m-%dT%H:%M:%S+00:00', 'now')",
];
