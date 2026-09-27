<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Security\Permissions as P;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Feature Manager. A feature list, assigned to a hosting plan, says which
 * panel features the plan's accounts may use. Enforcement is on the
 * server side: a feature's permissions are withheld from the account
 * (Authorizer), and routes of features that share a permission with
 * others are refused by name (Authenticate middlewares), so hiding menu
 * entries is a consequence, not the mechanism. A customer of a reseller
 * gets only what both its own plan and the reseller's plan allow. Staff
 * accounts (server-wide roles) are not affected.
 */
final class FeatureService
{
    /**
     * key => [label, permissions it grants, route-name prefixes it owns].
     */
    public const FEATURES = [
        'php' => ['PHP & website settings', [], ['sites.edit', 'sites.update']],
        'mysql' => ['MySQL / MariaDB databases', [P::DATABASES_VIEW, P::DATABASES_MANAGE], []],
        'email' => ['Email', [P::MAIL_MANAGE], []],
        'dns' => ['DNS zone editor', [P::DNS_MANAGE], []],
        'file_manager' => ['File manager', [P::FILES_MANAGE], []],
        'ssl' => ['SSL / HTTPS', [P::SSL_VIEW, P::SSL_MANAGE], []],
        'subdomains' => ['Subdomains & addon domains', [], ['sites.domains']],
        'ftp' => ['FTP / SFTP accounts', [P::FTP_MANAGE], []],
        'cron' => ['Cron jobs', [P::CRON_VIEW, P::CRON_MANAGE], []],
        'backups' => ['Backups', [P::BACKUPS_VIEW, P::BACKUPS_MANAGE], []],
        'apps' => ['Application installer', [P::APPS_INSTALL], []],
        'git' => ['Git deployment', [P::DEPLOY_MANAGE], []],
        'workers' => ['Workers (background processes)', [P::WORKERS_MANAGE], []],
        'redis' => ['Redis', [], ['sites.redis']],
        'logs' => ['Logs', [], ['sites.logs']],
        'stats' => ['Visitor statistics', [], ['sites.stats']],
        'malware' => ['Malware scan', [], ['sites.malware']],
        'ssh' => ['SSH shell & web terminal', [], ['sites.terminal']],
    ];

    /** @var array<int, list<string>> disabled features per user (request cache) */
    private array $cache = [];

    public function __construct(private readonly Connection $db)
    {
    }

    /**
     * Features turned off for a hosting account (empty for staff).
     *
     * @param array<string, mixed> $user
     * @return list<string>
     */
    public function disabledFor(array $user): array
    {
        if (($user['role_scope'] ?? 'all') !== 'own') {
            return [];
        }
        $id = (int) $user['id'];
        if (isset($this->cache[$id])) {
            return $this->cache[$id];
        }
        // The account's plan and, for a reseller's customer, the reseller's.
        $rows = $this->db->fetchAll(
            'SELECT DISTINCT d.feature FROM users u
             LEFT JOIN users r ON r.id = u.parent_id
             JOIN plans p ON p.id = u.plan_id OR p.id = r.plan_id
             JOIN feature_list_disabled d ON d.list_id = p.feature_list_id
             WHERE u.id = ?',
            [$id]
        );
        return $this->cache[$id] = array_values(array_intersect(array_column($rows, 'feature'), array_keys(self::FEATURES)));
    }

    /** @param array<string, mixed> $user */
    public function permissionAllowed(array $user, string $permission): bool
    {
        foreach ($this->disabledFor($user) as $f) {
            if (in_array($permission, self::FEATURES[$f][1], true)) {
                return false;
            }
        }
        return true;
    }

    /** @param array<string, mixed> $user */
    public function routeAllowed(array $user, ?string $route): bool
    {
        if ($route === null) {
            return true;
        }
        return $this->featureForRoute($route, $this->disabledFor($user)) === null;
    }

    /**
     * The disabled feature that owns a route, if any.
     *
     * @param list<string> $disabled
     */
    public function featureForRoute(string $route, array $disabled): ?string
    {
        foreach ($disabled as $f) {
            foreach (self::FEATURES[$f][2] as $prefix) {
                if ($route === $prefix || str_starts_with($route, $prefix . '.')) {
                    return $f;
                }
            }
        }
        return null;
    }

    /** @return list<array<string, mixed>> */
    public function lists(): array
    {
        $lists = $this->db->fetchAll('SELECT l.*, (SELECT COUNT(*) FROM plans p WHERE p.feature_list_id = l.id) AS plans FROM feature_lists l ORDER BY l.name');
        foreach ($lists as &$l) {
            $l['disabled'] = array_column($this->db->fetchAll('SELECT feature FROM feature_list_disabled WHERE list_id = ?', [(int) $l['id']]), 'feature');
        }
        return $lists;
    }

    /**
     * @param array<string, mixed> $input name, description, features[] (the enabled ones)
     */
    public function save(?int $id, array $input, int $actorId): int
    {
        $name = trim((string) ($input['name'] ?? ''));
        $desc = trim((string) ($input['description'] ?? ''));
        $errors = [];
        if ($name === '' || mb_strlen($name) > 60) {
            $errors['name'] = 'Enter a name (up to 60 characters).';
        } elseif ($this->db->scalar('SELECT id FROM feature_lists WHERE name = ? AND id != ?', [$name, $id ?? 0]) !== null) {
            $errors['name'] = 'A feature list with this name exists.';
        }
        if (mb_strlen($desc) > 300) {
            $errors['description'] = 'Keep the description under 300 characters.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $enabled = array_values(array_intersect(array_map('strval', (array) ($input['features'] ?? [])), array_keys(self::FEATURES)));
        $disabled = array_values(array_diff(array_keys(self::FEATURES), $enabled));
        $now = gmdate('c');
        if ($id === null) {
            $this->db->run('INSERT INTO feature_lists (name, description, created_at, updated_at) VALUES (?, ?, ?, ?)', [$name, $desc, $now, $now]);
            $id = $this->db->lastInsertId();
        } else {
            if ($this->db->scalar('SELECT id FROM feature_lists WHERE id = ?', [$id]) === null) {
                throw new UserFacingException('The feature list no longer exists.');
            }
            $this->db->run('UPDATE feature_lists SET name = ?, description = ?, updated_at = ? WHERE id = ?', [$name, $desc, $now, $id]);
        }
        $this->db->run('DELETE FROM feature_list_disabled WHERE list_id = ?', [$id]);
        foreach ($disabled as $f) {
            $this->db->run('INSERT INTO feature_list_disabled (list_id, feature) VALUES (?, ?)', [$id, $f]);
        }
        $this->cache = [];
        return $id;
    }

    /** @return string the removed list's name */
    public function delete(int $id): string
    {
        $name = $this->db->scalar('SELECT name FROM feature_lists WHERE id = ?', [$id]);
        if ($name === null) {
            throw new UserFacingException('The feature list no longer exists.');
        }
        // Plans using it fall back to every feature (ON DELETE SET NULL,
        // done explicitly in case foreign keys are off).
        $this->db->run('UPDATE plans SET feature_list_id = NULL WHERE feature_list_id = ?', [$id]);
        $this->db->run('DELETE FROM feature_lists WHERE id = ?', [$id]);
        $this->cache = [];
        return (string) $name;
    }
}
