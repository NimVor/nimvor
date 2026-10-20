-- generated from panel/database/migrations/0028_mail_routing.php — do not edit by hand
ALTER TABLE mail_domains ADD COLUMN routing TEXT NOT NULL DEFAULT 'local';

