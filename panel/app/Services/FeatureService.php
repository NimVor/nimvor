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
     * key => [label, permissions it grants, route-name prefixes it owns,
     * group]. The group is only for how the Feature Manager lays the list
     * out. A feature in DEFAULT_OFF is off unless a list opts in; every other
     * feature is on unless a list turns it off.
     */
    public const FEATURES = [
        'php' => ['PHP & website settings', [], ['sites.edit', 'sites.update'], 'Websites'],
        'mysql' => ['MySQL / MariaDB databases', [P::DATABASES_VIEW, P::DATABASES_MANAGE], [], 'Databases'],
        'mongodb' => ['MongoDB databases', [], ['sites.mongo'], 'Databases'],
        'email' => ['Email', [P::MAIL_MANAGE], [], 'Email'],
        'dns' => ['DNS zone editor', [P::DNS_MANAGE], [], 'Domains'],
        'file_manager' => ['File manager', [P::FILES_MANAGE], [], 'Files'],
        'ssl' => ['SSL / HTTPS', [P::SSL_VIEW, P::SSL_MANAGE], [], 'Websites'],
        'subdomains' => ['Subdomains', [], ['sites.subdomains'], 'Domains'],
        'addon_domains' => ['Addon domains', [], ['sites.addons'], 'Domains'],
        'cloudflare' => ['Cloudflare', [], ['sites.cloudflare'], 'Websites'],
        'ftp' => ['FTP / SFTP accounts', [P::FTP_MANAGE], [], 'Files'],
        'cron' => ['Cron jobs', [P::CRON_VIEW, P::CRON_MANAGE], [], 'Automation'],
        'backups' => ['Backups', [P::BACKUPS_VIEW, P::BACKUPS_MANAGE], [], 'Backups'],
        'auto_backups' => ['Show scheduled (automatic) backups to the customer', [], [], 'Backups'],
        'apps' => ['Application installer', [P::APPS_INSTALL], [], 'Websites'],
        'git' => ['Git deployment', [P::DEPLOY_MANAGE], [], 'Automation'],
        'workers' => ['Workers (background processes)', [P::WORKERS_MANAGE], [], 'Automation'],
        'redis' => ['Redis', [], ['sites.redis'], 'Caching'],
        'memcached' => ['Memcached', [], ['sites.memcached'], 'Caching'],
        'nodejs' => ['Node.js runtime & settings', [], ['sites.nodejs'], 'Runtimes'],
        'python' => ['Python runtime & settings', [], ['sites.python'], 'Runtimes'],
        'logs' => ['Logs', [], ['sites.logs'], 'Monitoring'],
        'stats' => ['Visitor statistics', [], ['sites.stats'], 'Monitoring'],
        'site_health' => ['Site health', [], ['sites.health'], 'Monitoring'],
        'malware' => ['Malware scan', [], ['sites.malware'], 'Security'],
        'firewall' => ['Web firewall (ModSecurity)', [], ['sites.firewall'], 'Security'],
        'site_security' => ['Site security overview', [], ['sites.security'], 'Security'],
        'ssh' => ['SSH shell & web terminal', [], ['sites.terminal'], 'Files'],
        'wordpress' => ['WordPress Toolkit', [], ['sites.wordpress', 'wordpress'], 'Websites'],
        // Which site types a customer may create (type_php and type_static are
        // on by default; the rest are opt-in). Enforced in the create form and
        // in WebsiteService.
        'type_php' => ['Create PHP sites', [], [], 'Site types'],
        'type_static' => ['Create static sites', [], [], 'Site types'],
        'type_nodejs' => ['Create Node.js sites', [], [], 'Site types'],
        'type_python' => ['Create Python sites', [], [], 'Site types'],
        'type_proxy' => ['Create reverse-proxy sites', [], [], 'Site types'],
    ];

    /**
     * Features that are OFF unless a list opts in (the reverse of the default).
     * They stay off for existing lists and for plans with no feature list.
     */
    public const DEFAULT_OFF = ['auto_backups', 'type_nodejs', 'type_python', 'type_proxy'];

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
        // The plan "slots" that apply to this account: its own, and (for a
        // reseller's customer) the reseller's. A null slot is a plan with no
        // feature list.
        $row = (array) $this->db->fetch(
            'SELECT u.parent_id, pu.feature_list_id AS own_list, pr.feature_list_id AS res_list
             FROM users u
             LEFT JOIN plans pu ON pu.id = u.plan_id
             LEFT JOIN users r ON r.id = u.parent_id
             LEFT JOIN plans pr ON pr.id = r.plan_id
             WHERE u.id = ?',
            [$id]
        );
        $slots = [$row['own_list'] ?? null];
        if (($row['parent_id'] ?? null) !== null) {
            $slots[] = $row['res_list'] ?? null;
        }
        $listIds = array_values(array_unique(array_map('intval', array_filter($slots, static fn ($v): bool => $v !== null))));

        // Normal features: off when any applicable list turns them off.
        $disabled = [];
        $optIn = [];
        if ($listIds !== []) {
            $place = implode(',', array_fill(0, count($listIds), '?'));
            $disabled = array_column($this->db->fetchAll("SELECT DISTINCT feature FROM feature_list_disabled WHERE list_id IN ($place)", $listIds), 'feature');
            foreach ($this->db->fetchAll("SELECT list_id, feature FROM feature_list_enabled WHERE list_id IN ($place)", $listIds) as $r) {
                $optIn[(int) $r['list_id']][(string) $r['feature']] = true;
            }
        }

        // Default-off features: on only when every applicable plan slot has a
        // list that opts in.
        foreach (self::DEFAULT_OFF as $f) {
            foreach ($slots as $listId) {
                if ($listId === null || empty($optIn[(int) $listId][$f])) {
                    $disabled[] = $f;
                    break;
                }
            }
        }

        return $this->cache[$id] = array_values(array_intersect(array_unique($disabled), array_keys(self::FEATURES)));
    }

    /**
     * Whether a single feature is on for this account (always on for staff).
     *
     * @param array<string, mixed> $user
     */
    public function enabledFor(array $user, string $feature): bool
    {
        return !in_array($feature, $this->disabledFor($user), true);
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
            $l['enabled_optin'] = array_column($this->db->fetchAll('SELECT feature FROM feature_list_enabled WHERE list_id = ?', [(int) $l['id']]), 'feature');
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
        // Normal features record what is OFF; default-off features record what
        // is opted IN.
        $normalKeys = array_values(array_diff(array_keys(self::FEATURES), self::DEFAULT_OFF));
        $disabled = array_values(array_diff($normalKeys, $enabled));
        $optIn = array_values(array_intersect($enabled, self::DEFAULT_OFF));
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
        $this->db->run('DELETE FROM feature_list_enabled WHERE list_id = ?', [$id]);
        foreach ($optIn as $f) {
            $this->db->run('INSERT INTO feature_list_enabled (list_id, feature) VALUES (?, ?)', [$id, $f]);
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
        $this->db->run('DELETE FROM feature_list_enabled WHERE list_id = ?', [$id]);
        $this->db->run('DELETE FROM feature_lists WHERE id = ?', [$id]);
        $this->cache = [];
        return (string) $name;
    }
}
