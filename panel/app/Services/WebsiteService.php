<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Support\Events;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * WhaCP-managed websites.
 *
 * The panel stores the desired state; the agent renders, validates and
 * applies it. If the agent rejects a change, the stored record is put back
 * to its previous values so panel and server never disagree.
 */
final class WebsiteService
{
    public const KINDS = ['php' => 'PHP application', 'static' => 'Static files', 'proxy' => 'Reverse proxy (Node.js, Python, …)'];

    public function __construct(
        private readonly WebsiteRepository $sites,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly Events $events,
        private readonly PlanLimits $limits,
        private readonly DiscoveryService $discovery,
        private readonly ?DnsService $dns = null,
        private readonly ?SettingsService $settings = null,
    ) {
    }

    /** @return list<string> installed PHP versions with FPM */
    public function phpVersions(): array
    {
        try {
            $php = (array) $this->agent->call('discovery.php');
        } catch (AgentException) {
            return [];
        }
        $out = [];
        foreach ((array) ($php['versions'] ?? []) as $v) {
            if (!empty($v['fpm_binary'])) {
                $out[] = (string) $v['version'];
            }
        }
        usort($out, 'version_compare');
        return $out;
    }

    /**
     * Hosting customers and resellers cannot add names under another
     * account's domain (x.their-domain.test): they would take its names.
     *
     * @param list<string> $names
     */
    private function assertNotUnderOthers(array $names, ?int $ownerId): void
    {
        $serverId = $this->server->id();
        foreach ($names as $n) {
            $parts = explode('.', $n);
            for ($i = 1; $i < count($parts) - 1; $i++) {
                $site = $this->sites->domainOwner($serverId, implode('.', array_slice($parts, $i)));
                if ($site === null) {
                    continue;
                }
                $owner = $this->sites->find($serverId, $site, null)['owner_id'] ?? null;
                if (($owner === null ? null : (int) $owner) !== $ownerId) {
                    throw new ValidationException([$n === $names[0] ? 'domain' : 'aliases' => "{$n} is under a domain of another account."]);
                }
            }
        }
    }

    /**
     * PHP-FPM settings only administrators (server-wide roles) change:
     * process counts and manager, request limits and disabling shell
     * functions.
     */
    public const SERVER_PHP_SETTINGS = ['max_children', 'harden', 'pm', 'start_servers', 'min_spare_servers', 'max_spare_servers', 'idle_timeout_seconds', 'max_requests', 'request_timeout_seconds', 'slowlog_seconds'];

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     */
    public function create(array $input, array $actor, ?array $parent = null): int
    {
        $data = $this->validate($input, null, Scope::isAll($actor));
        $serverId = $this->server->id();
        // A subdomain or addon domain belongs to its parent's owner.
        $ownerId = $parent !== null ? ($parent['owner_id'] === null ? null : (int) $parent['owner_id']) : $this->resolveOwner($input, $actor);
        $this->limits->assertCanCreate('websites', $ownerId);
        if (!Scope::isAll($actor)) {
            $this->assertNotUnderOthers($data['names'], $ownerId);
        }
        $newUser = $this->checkSystemUser((string) $data['row']['system_user'], $ownerId, $actor);

        $row = $data['row'] + ['server_id' => $serverId, 'owner_id' => $ownerId, 'status' => 'pending', 'parent_id' => $parent === null ? null : (int) $parent['id']];
        $id = $this->sites->insert($row);
        $this->sites->replaceDomains($serverId, $id, $data['names']);
        $site = $this->sites->find($serverId, $id, null);
        try {
            $this->apply($site, $newUser);
        } catch (\Throwable $e) {
            $this->sites->delete($id);
            $this->audit->record('website.create', 'website', $data['row']['domain'], AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            throw $e;
        }
        $this->audit->record('website.create', 'website', (string) $id, AuditLogger::SUCCESS, [
            'domain' => $row['domain'], 'kind' => $row['kind'], 'php' => $row['php_version'], 'user' => $row['system_user'],
        ]);
        $this->events->dispatch('website.created', ['website' => $this->sites->find($serverId, $id, null)]);
        return $id;
    }

    /**
     * @param array<string, mixed> $site current record (already access-checked)
     * @param array<string, mixed> $input
     */
    public function update(array $site, array $input, ?array $actor = null): void
    {
        $data = $this->validate($input, $site, $actor === null || Scope::isAll($actor));
        if ($actor !== null && !Scope::isAll($actor)) {
            $this->assertNotUnderOthers($data['names'], $site['owner_id'] === null ? null : (int) $site['owner_id']);
        }
        $id = (int) $site['id'];
        $previous = $site;
        $this->sites->update($id, $data['row']);
        $this->sites->replaceDomains((int) $site['server_id'], $id, $data['names']);
        try {
            $this->apply($this->sites->find((int) $site['server_id'], $id, null));
        } catch (\Throwable $e) {
            $this->restore($previous);
            throw $e;
        }
        $this->audit->record('website.update', 'website', (string) $id, AuditLogger::SUCCESS, [
            'domain' => $site['domain'], 'changes' => $this->diff($previous, $data['row']),
        ]);
        $this->events->dispatch('website.updated', ['website' => $this->sites->find((int) $site['server_id'], $id, null)]);
    }

    /**
     * Change fields that other features own (SSL, PHP version …) and apply.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $fields
     */
    public function patch(array $site, array $fields, string $auditAction): void
    {
        $id = (int) $site['id'];
        $this->sites->update($id, $fields);
        try {
            $this->apply($this->sites->find((int) $site['server_id'], $id, null));
        } catch (\Throwable $e) {
            $this->restore($site);
            throw $e;
        }
        $this->audit->record($auditAction, 'website', (string) $id, AuditLogger::SUCCESS, ['domain' => $site['domain'], 'changes' => $this->diff($site, $fields)]);        $this->events->dispatch('website.updated', ['website' => $this->sites->find((int) $site['server_id'], $id, null)]);
    }

    /** @param array<string, mixed> $site */
    public function setEnabled(array $site, bool $enabled): void
    {
        $this->patch($site, ['enabled' => $enabled], $enabled ? 'website.enable' : 'website.disable');
    }

    /**
     * @param array<string, mixed> $site
     * @return list<string> warnings
     */
    public function delete(array $site, bool $removeFiles, bool $removeUser): array
    {
        $id = (int) $site['id'];
        // The agent only sees websites with a PHP pool; static and disabled
        // websites have none. The panel knows them all: keep a shared account.
        $keptFor = array_values(array_filter($this->sites->usingSystemUser((int) $site['server_id'], (string) $site['system_user']), static fn (array $w): bool => (int) $w['id'] !== $id));
        $removeUser = $removeUser && $keptFor === [];
        $this->events->dispatch('website.deleting', ['website' => $site]);
        $res = (array) $this->agent->call('sites.delete', [
            'domain' => $site['domain'],
            'user' => $site['system_user'],
            'remove_files' => $removeFiles,
            'remove_user' => $removeUser,
        ]);
        $this->sites->delete($id);
        $this->discovery->invalidate();
        $this->audit->record('website.delete', 'website', (string) $id, AuditLogger::SUCCESS, [
            'domain' => $site['domain'], 'files' => $removeFiles ? ($res['trash'] ?? 'kept') : 'kept', 'user_removed' => $removeUser,
        ]);
        $this->events->dispatch('website.deleted', ['website' => $site]);
        $warnings = array_values(array_map('strval', (array) ($res['warnings'] ?? [])));
        if ($keptFor !== []) {
            $warnings[] = 'The system user ' . $site['system_user'] . ' was kept because other websites use it.';
        }
        return $warnings;
    }

    /** @param array<string, mixed> $site */
    public function restartPhp(array $site): void
    {
        if ($site['php_version'] === '') {
            throw new UserFacingException('This website does not use PHP.');
        }
        // The agent names the unit per distribution (php8.3-fpm, php83-php-fpm).
        $unit = 'php' . $site['php_version'] . '-fpm.service';
        foreach ((array) (((array) $this->agent->call('discovery.php'))['versions'] ?? []) as $v) {
            if (is_array($v) && ($v['version'] ?? '') === $site['php_version'] && !empty($v['fpm_service'])) {
                $unit = (string) $v['fpm_service'];
            }
        }
        $this->agent->call('services.control', ['unit' => $unit, 'op' => 'reload']);
        $this->audit->record('website.restart_php', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']]);
    }

    public function reloadNginx(): void
    {
        $this->agent->call('services.control', ['unit' => 'nginx.service', 'op' => 'reload']);
        $this->audit->record('service.reload', 'service', 'nginx.service', AuditLogger::SUCCESS);
    }

    /**
     * Re-apply the stored state (repairs drift, e.g. after a manual edit).
     *
     * @param array<string, mixed> $site
     */
    public function reapply(array $site): void
    {
        $this->apply($site);
        $this->audit->record('website.reapply', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']]);
    }

    /**
     * Agent spec for a stored website.
     *
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    public static function spec(array $site, bool $createUser): array
    {
        $php = (array) $site['php_settings'];
        return [
            'domain' => $site['domain'],
            'aliases' => array_values($site['aliases']),
            'user' => $site['system_user'],
            'create_user' => $createUser,
            'root' => $site['root'],
            'document_root' => $site['document_root'],
            'kind' => $site['kind'],
            'php_version' => $site['kind'] === 'php' ? $site['php_version'] : '',
            'php' => [
                'memory_limit_mb' => (int) ($php['memory_limit_mb'] ?? 256),
                'upload_max_mb' => (int) ($php['upload_max_mb'] ?? 64),
                'max_execution_seconds' => (int) ($php['max_execution_seconds'] ?? 60),
                'max_children' => (int) ($php['max_children'] ?? 5),
                'harden' => (bool) ($php['harden'] ?? false),
                'pm' => (string) ($php['pm'] ?? 'ondemand'),
                'start_servers' => (int) ($php['start_servers'] ?? 0),
                'min_spare_servers' => (int) ($php['min_spare_servers'] ?? 0),
                'max_spare_servers' => (int) ($php['max_spare_servers'] ?? 0),
                'idle_timeout_seconds' => (int) ($php['idle_timeout_seconds'] ?? 10),
                'max_requests' => (int) ($php['max_requests'] ?? 500),
                'request_timeout_seconds' => (int) ($php['request_timeout_seconds'] ?? 0),
                'slowlog_seconds' => (int) ($php['slowlog_seconds'] ?? 10),
            ] + (!empty($php['ini']) ? ['ini' => (array) $php['ini']] : []),
            'proxy_pass' => $site['kind'] === 'proxy' ? $site['proxy_pass'] : '',
            'https' => (bool) $site['https'],
            'force_https' => (bool) $site['force_https'],
            'hsts' => (bool) $site['hsts'],
            'cert_file' => $site['cert_file'],
            'key_file' => $site['key_file'],
            'enabled' => (bool) $site['enabled'],
            'redirects' => array_values((array) $site['redirects']),
            'client_max_body_mb' => (int) $site['client_max_body_mb'],
        ];
    }

    /** @param array<string, mixed>|null $site */
    /**
     * Whether the website's system account may be used, and whether it must
     * be new. Hosting customers and resellers may reuse only the account of
     * one of their own websites with the same owner (subdomains, addon
     * domains); any other name must not exist on the server yet, so another
     * customer's account or a server login can never be taken over.
     * Administrators may attach existing accounts, as before.
     *
     * @param array<string, mixed> $actor
     * @return bool true: the agent must create the account (refuse an existing one)
     */
    private function checkSystemUser(string $user, ?int $ownerId, array $actor): bool
    {
        $usedBy = $this->sites->usingSystemUser($this->server->id(), $user);
        if (\WhaCP\Hosting\Scope::isAll($actor)) {
            return false;
        }
        if ($usedBy === []) {
            return true;
        }
        foreach ($usedBy as $w) {
            if ($this->sites->find($this->server->id(), (int) $w['id'], $actor) === null || (int) $w['owner_id'] !== (int) $ownerId) {
                throw new ValidationException(['system_user' => 'The system user ' . $user . ' belongs to another account. Choose another name.']);
            }
        }
        return false;
    }

    /**
     * A subdomain (name.parent-domain) or addon domain (any other domain)
     * of a website: a website of its own, with the parent's account, owner
     * and PHP version; the subdomain also gets an A record when the parent's
     * DNS zone is hosted here.
     *
     * @param array<string, mixed> $parent access-checked
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{id: int, dns: bool}
     */
    public function createChild(array $parent, string $type, array $input, array $actor): array
    {
        if ($type === 'subdomain') {
            $label = strtolower(trim((string) ($input['subdomain'] ?? '')));
            if (!preg_match('/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/', $label) || in_array($label, ['www'], true)) {
                throw new ValidationException(['subdomain' => 'Use letters, digits and hyphens, e.g. blog or shop (www is an alias of the website itself).']);
            }
            $domain = $label . '.' . $parent['domain'];
        } else {
            $domain = self::normalizeHost((string) ($input['domain'] ?? ''));
            if ($domain === $parent['domain'] || str_ends_with($domain, '.' . $parent['domain'])) {
                throw new ValidationException(['domain' => 'That is a subdomain of ' . $parent['domain'] . '; add it as a subdomain.']);
            }
        }
        $child = [
            'domain' => $domain,
            'system_user' => (string) $parent['system_user'],
            'kind' => (string) $parent['kind'] === 'proxy' ? 'php' : (string) $parent['kind'],
            'php_version' => (string) ($parent['php_version'] ?: ''),
            'aliases' => $type === 'addon' && !empty($input['www']) ? 'www.' . $domain : '',
        ];
        if ($child['kind'] === 'php' && $child['php_version'] === '') {
            $child['php_version'] = $this->phpVersions()[0] ?? '';
        }
        $id = $this->create($child, $actor, $parent);
        $dns = false;
        if ($type === 'subdomain' && !empty($input['dns'])) {
            $dns = $this->addSubdomainRecord($parent, $label ?? '');
        }
        return ['id' => $id, 'dns' => $dns];
    }

    /** @param array<string, mixed> $parent */
    private function addSubdomainRecord(array $parent, string $label): bool
    {
        if ($this->dns === null || $this->settings === null) {
            return false;
        }
        $zone = $this->dns->forWebsite((int) $parent['id']);
        $ip = trim($this->settings->string('dns_default_ipv4'));
        if ($zone === null || filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false) {
            return false;
        }
        foreach ($this->dns->records((int) $zone['id']) as $r) {
            if ((string) $r['name'] === $label) {
                return false;
            }
        }
        try {
            $this->dns->saveRecord($zone, null, ['name' => $label, 'type' => 'A', 'value' => $ip, 'ttl' => 0]);
        } catch (\Throwable) {
            return false; // the website exists; the record can be added on the DNS page
        }
        return true;
    }

    private function apply(?array $site, bool $newUser = false): void
    {
        if ($site === null) {
            throw new UserFacingException('The website record disappeared while saving.');
        }
        // create_user: the agent recreates a missing site account;
        // new_user: it must not exist yet (see checkSystemUser).
        try {
            $this->agent->call('sites.apply', self::spec($site, true) + ($newUser ? ['new_user' => true] : []));
        } finally {
            $this->discovery->invalidate();
        }
        $this->sites->setStatus((int) $site['id'], $site['enabled'] ? 'active' : 'disabled');
    }

    /** @param array<string, mixed> $previous */
    private function restore(array $previous): void
    {
        $id = (int) $previous['id'];
        $fields = array_intersect_key($previous, array_flip([
            'domain', 'system_user', 'root', 'document_root', 'kind', 'php_version', 'php_settings', 'proxy_pass',
            'https', 'force_https', 'hsts', 'ssl_mode', 'cert_file', 'key_file', 'redirects', 'client_max_body_mb', 'enabled', 'status',
        ]));
        $this->sites->update($id, $fields);
        $this->sites->replaceDomains((int) $previous['server_id'], $id, array_merge([(string) $previous['domain']], $previous['aliases']));
    }

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     */
    private function resolveOwner(array $input, array $actor): ?int
    {
        $requested = filter_var($input['owner_id'] ?? null, FILTER_VALIDATE_INT);
        if (($actor['role_scope'] ?? 'all') === 'all') {
            return $requested !== false && $requested > 0 ? $requested : null;
        }
        return (int) $actor['id']; // customers and resellers own what they create
    }

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed>|null $existing
     * @return array{row: array<string, mixed>, names: list<string>}
     */
    public function validate(array $input, ?array $existing, bool $serverSettings = true): array
    {
        if (!$serverSettings) {
            // Process limits and hardening are the administrator's: hosting
            // customers and resellers keep the stored (or default) values.
            foreach (self::SERVER_PHP_SETTINGS as $k) {
                unset($input[$k]);
            }
        }
        $errors = [];
        $serverId = $this->server->id();
        $domain = $existing['domain'] ?? self::normalizeHost((string) ($input['domain'] ?? ''));
        if ($existing === null && !self::validHost($domain)) {
            $errors['domain'] = 'Enter a valid domain name such as example.com.';
        }
        $aliases = [];
        foreach (preg_split('/[\s,]+/', (string) ($input['aliases'] ?? ''), -1, PREG_SPLIT_NO_EMPTY) ?: [] as $a) {
            $a = self::normalizeHost($a);
            if (!self::validHost($a)) {
                $errors['aliases'] = "“{$a}” is not a valid domain name.";
                break;
            }
            if ($a !== $domain && !in_array($a, $aliases, true)) {
                $aliases[] = $a;
            }
        }
        if (count($aliases) > 50) {
            $errors['aliases'] = 'At most 50 aliases are allowed.';
        }
        $names = array_merge([$domain], $aliases);
        foreach ($names as $n) {
            $owner = $this->sites->domainOwner($serverId, $n);
            if ($owner !== null && $owner !== (int) ($existing['id'] ?? 0)) {
                $errors[$n === $domain ? 'domain' : 'aliases'] = "{$n} already belongs to another website.";
            }
        }

        $kind = (string) ($input['kind'] ?? $existing['kind'] ?? 'php');
        if (!isset(self::KINDS[$kind])) {
            $errors['kind'] = 'Choose a website type.';
        }
        $php = '';
        if ($kind === 'php') {
            $php = (string) ($input['php_version'] ?? $existing['php_version'] ?? '');
            $available = $this->phpVersions();
            if (!in_array($php, $available, true)) {
                $errors['php_version'] = $available === [] ? 'No PHP-FPM version is installed. Install one on the PHP page first.' : 'Choose an installed PHP version.';
            }
        }
        $proxy = '';
        if ($kind === 'proxy') {
            $proxy = trim((string) ($input['proxy_pass'] ?? ''));
            if (!preg_match('#^http://(127\.0\.0\.1|\[::1\]|localhost):[0-9]{2,5}(/[A-Za-z0-9._~/-]*)?$#', $proxy)) {
                $errors['proxy_pass'] = 'Enter a local address such as http://127.0.0.1:3000.';
            }
        }

        $user = $existing['system_user'] ?? strtolower(trim((string) ($input['system_user'] ?? '')));
        if ($existing === null) {
            if ($user === '') {
                $user = self::suggestUser($domain);
            }
            if (!preg_match('/^[a-z][a-z0-9_-]{1,30}$/', $user) || in_array($user, ['root', 'www-data', 'nobody', 'mysql', 'whacp', 'admin'], true)) {
                $errors['system_user'] = 'Use 2–31 lowercase letters, digits, - or _, starting with a letter.';
            }
        }

        $ints = [
            'memory_limit_mb' => [32, 8192, 256], 'upload_max_mb' => [1, 4096, 64],
            'max_execution_seconds' => [5, 3600, 60], 'max_children' => [1, 200, 5],
            // PHP-FPM process manager (0: derived by the agent).
            'start_servers' => [0, 200, 0], 'min_spare_servers' => [0, 200, 0], 'max_spare_servers' => [0, 200, 0],
            'idle_timeout_seconds' => [1, 3600, 10], 'max_requests' => [1, 100000, 500],
            'request_timeout_seconds' => [0, 86400, 0], 'slowlog_seconds' => [1, 3600, 10],
        ];
        $settings = [];
        foreach ($ints as $k => [$lo, $hi, $def]) {
            $raw = $input[$k] ?? ($existing['php_settings'][$k] ?? $def);
            $v = filter_var($raw, FILTER_VALIDATE_INT);
            if ($v === false || $v < $lo || $v > $hi) {
                $errors[$k] = "Enter a number between {$lo} and {$hi}.";
                $v = $def;
            }
            $settings[$k] = $v;
        }
        $settings['harden'] = filter_var($input['harden'] ?? ($existing['php_settings']['harden'] ?? false), FILTER_VALIDATE_BOOL);
        $pm = (string) ($input['pm'] ?? ($existing['php_settings']['pm'] ?? 'ondemand'));
        if (!in_array($pm, ['ondemand', 'dynamic', 'static'], true)) {
            $errors['pm'] = 'Choose on demand, dynamic or static.';
            $pm = 'ondemand';
        }
        $settings['pm'] = $pm;
        if ($pm === 'dynamic') {
            [$min, $start, $max] = [$settings['min_spare_servers'], $settings['start_servers'], $settings['max_spare_servers']];
            $explicit = array_filter([$min, $start, $max]) !== [];
            if ($explicit && !(1 <= $min && $min <= $start && $start <= $max && $max <= $settings['max_children'])) {
                $errors['start_servers'] = 'Dynamic needs 1 ≤ min spare ≤ start ≤ max spare ≤ max PHP processes (or leave all three at 0).';
            }
        }
        // The website's own php.ini directives.
        $settings['ini'] = (array) ($existing['php_settings']['ini'] ?? []);
        if (is_array($input['ini'] ?? null)) {
            try {
                $settings['ini'] = PhpDirectives::validate($input['ini'], $existing['root'] ?? rtrim($this->sitesRoot(), '/') . '/' . $domain);
            } catch (ValidationException $e) {
                $errors += $e->errors();
            }
        }
        $body = filter_var($input['client_max_body_mb'] ?? ($existing['client_max_body_mb'] ?? $settings['upload_max_mb']), FILTER_VALIDATE_INT);
        if ($body === false || $body < 1 || $body > 4096) {
            $errors['client_max_body_mb'] = 'Enter a number between 1 and 4096.';
        }

        $redirects = [];
        foreach (preg_split('/\R/', (string) ($input['redirects'] ?? self::redirectsText($existing['redirects'] ?? []))) ?: [] as $i => $line) {
            $line = trim($line);
            if ($line === '' || str_starts_with($line, '#')) {
                continue;
            }
            $parts = preg_split('/\s+/', $line) ?: [];
            [$from, $to, $code] = [$parts[0] ?? '', $parts[1] ?? '', (int) ($parts[2] ?? 301)];
            if (!preg_match('#^/[A-Za-z0-9._~/-]{0,200}$#', $from) || !preg_match('#^(https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?)?(/[A-Za-z0-9._~/%?=&+-]{0,300})?$#', $to) || $to === '' || !in_array($code, [301, 302, 307, 308], true)) {
                $errors['redirects'] = 'Line ' . ($i + 1) . ': use “/old-path https://example.com/new [301]”.';
                break;
            }
            $redirects[] = ['from' => $from, 'to' => $to, 'code' => $code];
        }

        $sub = trim((string) ($input['document_root_sub'] ?? ''), '/');
        if ($sub !== '' && (!preg_match('#^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$#', $sub) || preg_match('#(^|/)\.\.?(/|$)#', $sub))) {
            $errors['document_root_sub'] = 'Use a relative folder such as public_html/public.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }

        $root = $existing['root'] ?? '';
        $docroot = $existing['document_root'] ?? '';
        if ($existing === null) {
            $root = rtrim($this->sitesRoot(), '/') . '/' . $domain;
            $docroot = $root . '/public_html';
        }
        if ($sub !== '') {
            $docroot = $root . '/' . $sub;
        }

        $row = [
            'domain' => $domain,
            'system_user' => $user,
            'root' => $root,
            'document_root' => $docroot,
            'kind' => $kind,
            'php_version' => $php,
            'php_settings' => $settings,
            'proxy_pass' => $proxy,
            'redirects' => $redirects,
            'client_max_body_mb' => (int) $body,
            'enabled' => $existing === null ? true : (bool) $existing['enabled'],
        ];
        return ['row' => $row, 'names' => $names];
    }

    private function sitesRoot(): string
    {
        try {
            $s = (array) $this->agent->call('agent.status');
            return (string) ($s['sites_root'] ?? '/home/whacp');
        } catch (AgentException) {
            return '/home/whacp';
        }
    }

    public static function normalizeHost(string $h): string
    {
        $h = strtolower(trim($h, " \t\n\r\0\x0B."));
        if ($h !== '' && preg_match('/[^\x20-\x7e]/', $h) && function_exists('idn_to_ascii')) {
            $ascii = idn_to_ascii($h, IDNA_DEFAULT, INTL_IDNA_VARIANT_UTS46);
            if (is_string($ascii)) {
                $h = $ascii;
            }
        }
        return $h;
    }

    public static function validHost(string $h): bool
    {
        if (strlen($h) < 3 || strlen($h) > 253 || !str_contains($h, '.') || filter_var($h, FILTER_VALIDATE_IP)) {
            return false;
        }
        foreach (explode('.', $h) as $label) {
            if (!preg_match('/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/', $label)) {
                return false;
            }
        }
        return true;
    }

    public static function suggestUser(string $domain): string
    {
        $base = preg_replace('/[^a-z0-9]/', '', explode('.', $domain)[0]) ?: 'site';
        if (!preg_match('/^[a-z]/', $base)) {
            $base = 's' . $base;
        }
        if (strlen($base) < 3) {
            $base .= 'site';
        }
        return substr($base, 0, 24);
    }

    /** @param list<array<string, mixed>> $redirects */
    public static function redirectsText(array $redirects): string
    {
        return implode("\n", array_map(static fn (array $r): string => $r['from'] . ' ' . $r['to'] . ' ' . $r['code'], $redirects));
    }

    /**
     * @param array<string, mixed> $before
     * @param array<string, mixed> $after
     * @return array<string, array{from: mixed, to: mixed}>
     */
    private function diff(array $before, array $after): array
    {
        $out = [];
        foreach ($after as $k => $v) {
            if (array_key_exists($k, $before) && $before[$k] != $v) {
                $out[$k] = ['from' => $before[$k], 'to' => $v];
            }
        }
        return $out;
    }
}
