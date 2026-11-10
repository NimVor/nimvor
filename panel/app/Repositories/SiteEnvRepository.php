<?php

declare(strict_types=1);

namespace NimVor\Repositories;

use NimVor\Database\Connection;
use NimVor\Security\Crypto;

/** A website's environment variables, kept encrypted (one blob per website). */
final class SiteEnvRepository
{
    public function __construct(private readonly Connection $db, private readonly Crypto $crypto)
    {
    }

    /** @return array<string, string> */
    public function get(int $websiteId): array
    {
        $enc = $this->db->scalar('SELECT vars_enc FROM site_env WHERE website_id = ?', [$websiteId]);
        if (!is_string($enc) || $enc === '') {
            return [];
        }
        $vars = json_decode($this->crypto->decrypt($enc), true);
        if (!is_array($vars)) {
            return [];
        }
        $out = [];
        foreach ($vars as $k => $v) {
            $out[(string) $k] = (string) $v;
        }
        return $out;
    }

    /** @param array<string, string> $vars */
    public function set(int $websiteId, array $vars): void
    {
        if ($vars === []) {
            $this->db->run('DELETE FROM site_env WHERE website_id = ?', [$websiteId]);
            return;
        }
        $enc = $this->crypto->encrypt((string) json_encode($vars, JSON_THROW_ON_ERROR));
        $this->db->run(
            'INSERT INTO site_env (website_id, vars_enc, updated_at) VALUES (?, ?, ?) ON CONFLICT(website_id) DO UPDATE SET vars_enc = excluded.vars_enc, updated_at = excluded.updated_at',
            [$websiteId, $enc, gmdate('c')]
        );
    }
}
