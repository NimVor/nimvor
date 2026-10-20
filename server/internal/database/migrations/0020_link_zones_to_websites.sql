-- generated from panel/database/migrations/0020_link_zones_to_websites.php — do not edit by hand
UPDATE dns_zones SET
    website_id = (SELECT w.id FROM websites w WHERE w.server_id = dns_zones.server_id AND w.domain = dns_zones.name),
    owner_id = COALESCE(owner_id, (SELECT w.owner_id FROM websites w WHERE w.server_id = dns_zones.server_id AND w.domain = dns_zones.name))
WHERE website_id IS NULL
  AND EXISTS (SELECT 1 FROM websites w WHERE w.server_id = dns_zones.server_id AND w.domain = dns_zones.name
              AND (dns_zones.owner_id IS NULL OR dns_zones.owner_id IS w.owner_id));

UPDATE mail_domains SET
    website_id = (SELECT w.id FROM websites w WHERE w.server_id = mail_domains.server_id AND w.domain = mail_domains.domain),
    owner_id = COALESCE(owner_id, (SELECT w.owner_id FROM websites w WHERE w.server_id = mail_domains.server_id AND w.domain = mail_domains.domain))
WHERE website_id IS NULL
  AND EXISTS (SELECT 1 FROM websites w WHERE w.server_id = mail_domains.server_id AND w.domain = mail_domains.domain
              AND (mail_domains.owner_id IS NULL OR mail_domains.owner_id IS w.owner_id));

