<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Email domains, mailboxes and aliases. The agent configures the mail
 * server and keeps the password hashes; the panel keeps who owns what.
 */
final class MailService
{
    public const MAX_MAILBOXES = 1000;
    private const LOCAL = '/^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$/';

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly WebsiteRepository $sites,
        private readonly DnsService $dns,
        private readonly ?PlanLimits $limits = null,
        private readonly ?\WhaCP\Security\PasswordPolicy $policy = null,
    ) {
    }

    /** @return array<string, mixed>|null */
    public function status(): ?array
    {
        try {
            return (array) $this->agent->call('mail.status');
        } catch (AgentException) {
            return null;
        }
    }

    /**
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function domains(array $user): array
    {
        [$cond, $params] = Scope::withWebsite($user, 'd.owner_id', 'd.website_id');
        return $this->db->fetchAll(
            "SELECT d.*, w.domain AS website,
                (SELECT COUNT(*) FROM mail_mailboxes m WHERE m.domain_id = d.id) AS mailboxes,
                (SELECT COUNT(*) FROM mail_aliases a WHERE a.domain_id = d.id) AS aliases
             FROM mail_domains d LEFT JOIN websites w ON w.id = d.website_id
             WHERE d.server_id = :server AND {$cond} ORDER BY d.domain",
            ['server' => $this->server->id()] + $params
        );
    }

    /**
     * @param array<string, mixed> $user
     * @return array<string, mixed>|null
     */
    public function domain(int $id, array $user): ?array
    {
        [$cond, $params] = Scope::withWebsite($user, 'owner_id', 'website_id');
        return $this->db->fetch("SELECT * FROM mail_domains WHERE id = :id AND server_id = :server AND {$cond}", ['id' => $id, 'server' => $this->server->id()] + $params);
    }

    public function forWebsite(int $websiteId): ?array
    {
        return $this->db->fetch('SELECT * FROM mail_domains WHERE website_id = ?', [$websiteId]);
    }

    /** @return list<array<string, mixed>> */
    public function mailboxes(int $domainId): array
    {
        return $this->db->fetchAll('SELECT * FROM mail_mailboxes WHERE domain_id = ? ORDER BY local', [$domainId]);
    }

    /** @return list<array<string, mixed>> */
    public function aliases(int $domainId): array
    {
        return $this->db->fetchAll("SELECT * FROM mail_aliases WHERE domain_id = ? ORDER BY local = '', local", [$domainId]);
    }

    /**
     * @param array<string, mixed> $input website_id, domain (administrators)
     * @param array<string, mixed> $actor
     */
    public function addDomain(array $input, array $actor): int
    {
        $websiteId = filter_var($input['website_id'] ?? null, FILTER_VALIDATE_INT) ?: null;
        $site = $websiteId !== null ? $this->sites->find($this->server->id(), $websiteId, $actor) : null;
        if ($websiteId !== null && $site === null) {
            throw new ValidationException(['website_id' => 'Choose one of your websites.']);
        }
        $domain = strtolower(trim((string) ($input['domain'] ?? '')));
        if ($domain === '' && $site !== null) {
            $domain = strtolower((string) $site['domain']);
        }
        if (!Scope::isAll($actor) && ($site === null || strtolower((string) $site['domain']) !== $domain)) {
            throw new ValidationException(['website_id' => 'Choose the website whose domain receives email.']);
        }
        if (!preg_match('/^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/', $domain)) {
            throw new ValidationException(['domain' => 'Enter a domain name such as example.com.']);
        }
        // Email for a website's domain belongs to that website (and so to
        // its owner), whether or not the form named the website.
        $site ??= Scope::isAll($actor) ? $this->sites->findByDomain($this->server->id(), $domain) : null;
        if ($this->db->scalar('SELECT COUNT(*) FROM mail_domains WHERE server_id = ? AND domain = ?', [$this->server->id(), $domain]) > 0) {
            throw new ValidationException(['domain' => 'Email is already hosted for this domain.']);
        }
        $res = (array) $this->agent->call('mail.domain_add', ['domain' => $domain], 60);
        $ownerId = $site !== null ? ($site['owner_id'] !== null ? (int) $site['owner_id'] : null) : (Scope::isAll($actor) ? null : (int) $actor['id']);
        $this->db->run(
            'INSERT INTO mail_domains (server_id, domain, website_id, owner_id, dkim_selector, dkim_txt, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), $domain, $site['id'] ?? null, $ownerId, (string) ($res['dkim_selector'] ?? ''), (string) ($res['dkim_txt'] ?? ''), gmdate('c')]
        );
        $id = $this->db->lastInsertId();
        if ($ownerId !== null) {
            $this->syncSpam(); // the account's spam options cover the new domain
        }
        $this->audit->record('mail.domain_add', 'mail_domain', (string) $id, AuditLogger::SUCCESS, ['domain' => $domain]);
        return $id;
    }

    /**
     * Domains whose accounts turned the spam filter off, and whose
     * accounts keep marked mail in the inbox (Spam Box off).
     *
     * @return array{no_filter_domains: list<string>, no_junk_domains: list<string>}
     */
    public function spamExceptions(): array
    {
        $out = ['no_filter_domains' => [], 'no_junk_domains' => []];
        $options = new AccountOptions($this->db);
        foreach ($this->db->fetchAll('SELECT domain, owner_id FROM mail_domains WHERE server_id = ? ORDER BY domain', [$this->server->id()]) as $d) {
            $o = $options->get($d['owner_id'] === null ? null : (int) $d['owner_id']);
            if (!$o['spam_filter']) {
                $out['no_filter_domains'][] = (string) $d['domain'];
            }
            if (!$o['spam_box']) {
                $out['no_junk_domains'][] = (string) $d['domain'];
            }
        }
        return $out;
    }

    /**
     * Apply the accounts' spam options to the server's spam set-up (when
     * email is set up). Returns false when it could not be applied.
     */
    public function syncSpam(): bool
    {
        $status = $this->status();
        if ($status === null || empty($status['configured'])) {
            return true;
        }
        $spam = (array) ($status['spam'] ?? []);
        try {
            $this->agent->call('mail.spam_configure', [
                'enabled' => !empty($spam['enabled']),
                'add_header' => (float) ($spam['add_header'] ?? 6),
                'reject' => (float) ($spam['reject'] ?? 15),
            ] + $this->spamExceptions(), 120);
        } catch (AgentException) {
            return false;
        }
        return true;
    }

    /**
     * DNS records the domain needs for email.
     *
     * @param array<string, mixed> $domain
     * @return list<array{name: string, type: string, value: string, priority: int, note: string}>
     */
    public function dnsRecords(array $domain, string $mailHost): array
    {
        $out = [];
        if ($mailHost !== '') {
            $out[] = ['name' => '@', 'type' => 'MX', 'value' => $mailHost . '.', 'priority' => 10, 'note' => 'Mail for the domain goes to this server.'];
        }
        $out[] = ['name' => '@', 'type' => 'TXT', 'value' => 'v=spf1 a mx ~all', 'priority' => 0, 'note' => 'SPF: this server may send for the domain.'];
        if ($domain['dkim_selector'] !== '') {
            $out[] = ['name' => $domain['dkim_selector'] . '._domainkey', 'type' => 'TXT', 'value' => (string) $domain['dkim_txt'], 'priority' => 0, 'note' => 'DKIM: the public key for signatures.'];
        }
        $out[] = ['name' => '_dmarc', 'type' => 'TXT', 'value' => 'v=DMARC1; p=none; rua=mailto:postmaster@' . $domain['domain'], 'priority' => 0, 'note' => 'DMARC: start with p=none and tighten once mail flows.'];
        return $out;
    }

    /**
     * Add the missing email records to the domain's DNS zone on this panel.
     *
     * @param array<string, mixed> $domain
     * @param array<string, mixed> $actor
     */
    public function publishDns(array $domain, string $mailHost, array $actor): int
    {
        $zone = null;
        foreach ($this->dns->zones($actor) as $z) {
            if ($z['name'] === $domain['domain']) {
                $zone = $this->dns->zone((int) $z['id'], $actor);
            }
        }
        if ($zone === null) {
            throw new UserFacingException('There is no DNS zone for ' . $domain['domain'] . ' on this panel.', '', 'Create the zone under DNS, or add the records at your DNS provider.');
        }
        $existing = $this->dns->records((int) $zone['id']);
        $added = 0;
        foreach ($this->dnsRecords($domain, $mailHost) as $r) {
            foreach ($existing as $e) {
                $sameSpf = $r['type'] === 'TXT' && str_starts_with($r['value'], 'v=spf1') && str_starts_with((string) $e['value'], 'v=spf1');
                if ($e['name'] === $r['name'] && $e['type'] === $r['type'] && ($r['type'] !== 'TXT' || $sameSpf || $e['value'] === $r['value'] || str_starts_with((string) $e['value'], 'v=DMARC1'))) {
                    continue 2;
                }
            }
            $this->dns->saveRecord($zone, null, $r + ['ttl' => 0]);
            $added++;
        }
        return $added;
    }

    /** @param array<string, mixed> $domain */
    public function removeDomain(array $domain, string $confirm): void
    {
        if (strtolower(trim($confirm)) !== $domain['domain']) {
            throw new ValidationException(['confirm' => 'Type the domain to confirm.']);
        }
        if ($this->mailboxes((int) $domain['id']) !== [] || $this->aliases((int) $domain['id']) !== []) {
            throw new UserFacingException('Delete the domain\'s mailboxes and aliases first.');
        }
        try {
            $this->agent->call('mail.domain_remove', ['domain' => $domain['domain'], 'confirm' => $domain['domain']]);
        } catch (AgentException $e) {
            if ($e->status() !== 404) {
                throw $e;
            }
        }
        $this->db->run('DELETE FROM mail_domains WHERE id = ?', [$domain['id']]);
        $this->audit->record('mail.domain_remove', 'mail_domain', (string) $domain['id'], AuditLogger::SUCCESS, ['domain' => $domain['domain']]);
    }

    /**
     * Create or change a mailbox. Returns the generated password, if any.
     *
     * @param array<string, mixed> $domain
     * @param array<string, mixed> $input local, password, generate, quota_mb
     */
    public function saveMailbox(array $domain, ?int $mailboxId, array $input): ?string
    {
        $existing = $mailboxId !== null ? $this->db->fetch('SELECT * FROM mail_mailboxes WHERE id = ? AND domain_id = ?', [$mailboxId, $domain['id']]) : null;
        if ($mailboxId !== null && $existing === null) {
            throw new UserFacingException('The mailbox no longer exists.');
        }
        $local = $existing !== null ? (string) $existing['local'] : strtolower(trim((string) ($input['local'] ?? '')));
        if (!preg_match(self::LOCAL, $local) || str_contains($local, '..')) {
            throw new ValidationException(['local' => 'Use letters, digits and . _ + - (not at the start or end).']);
        }
        $password = is_string($input['password'] ?? null) ? (string) $input['password'] : '';
        $generated = null;
        if (!empty($input['generate'])) {
            $password = $generated = $this->policy?->generate(20, ':') ?? rtrim(strtr(base64_encode(random_bytes(15)), '+/', 'Aa'), '=');
        }
        if ($password !== '' && (strlen($password) < 10 || strlen($password) > 128 || str_contains($password, ':'))) {
            throw new ValidationException(['password' => 'Use 10–128 characters (no colon), or generate one.']);
        }
        // Webmail sign-ins use this password: the server's password policy
        // applies to passwords typed in (generated ones meet it).
        if ($password !== '' && $generated === null && $this->policy !== null && $this->policy->enabled() && ($weak = $this->policy->check($password)) !== null) {
            throw new ValidationException(['password' => $weak]);
        }
        if ($existing === null && $password === '') {
            throw new ValidationException(['password' => 'Enter a password or generate one.']);
        }
        $quota = max(0, (int) ($input['quota_mb'] ?? 0));
        $owner = $domain['owner_id'] === null ? null : (int) $domain['owner_id'];
        if ($this->limits !== null && ($max = $this->limits->value($owner, 'max_email_quota_mb')) > 0) {
            // The plan caps each mailbox; "unlimited" becomes the cap.
            if ($quota > $max) {
                throw new ValidationException(['quota_mb' => "Your plan allows at most {$max} MB per mailbox."]);
            }
            $quota = $quota === 0 ? $max : $quota;
        }
        if ($existing === null) {
            $this->limits?->assertCanCreate('email_accounts', $owner);
            if ($this->db->scalar('SELECT COUNT(*) FROM mail_mailboxes WHERE domain_id = ?', [$domain['id']]) >= self::MAX_MAILBOXES) {
                throw new UserFacingException('This domain has the maximum number of mailboxes.');
            }
            if ($this->db->scalar('SELECT COUNT(*) FROM mail_mailboxes WHERE domain_id = ? AND local = ?', [$domain['id'], $local]) > 0
                || $this->db->scalar('SELECT COUNT(*) FROM mail_aliases WHERE domain_id = ? AND local = ?', [$domain['id'], $local]) > 0) {
                throw new ValidationException(['local' => 'This address exists already.']);
            }
        }
        $address = $local . '@' . $domain['domain'];
        $params = ['address' => $address, 'quota_mb' => $quota];
        if ($password !== '') {
            $params['password'] = $password;
        }
        $this->agent->call('mail.mailbox_save', $params, 60);
        if ($existing === null) {
            $this->db->run('INSERT INTO mail_mailboxes (domain_id, local, quota_mb, created_at) VALUES (?, ?, ?, ?)', [$domain['id'], $local, $quota, gmdate('c')]);
        } else {
            $this->db->run('UPDATE mail_mailboxes SET quota_mb = ? WHERE id = ?', [$quota, $existing['id']]);
        }
        $this->audit->record($existing === null ? 'mail.mailbox_create' : 'mail.mailbox_update', 'mail_domain', (string) $domain['id'], AuditLogger::SUCCESS, ['address' => $address, 'quota_mb' => $quota, 'password_changed' => $password !== '']);
        return $generated;
    }

    /** @param array<string, mixed> $domain */
    public function deleteMailbox(array $domain, int $mailboxId, string $confirm): void
    {
        $m = $this->db->fetch('SELECT * FROM mail_mailboxes WHERE id = ? AND domain_id = ?', [$mailboxId, $domain['id']]) ?? throw new UserFacingException('The mailbox no longer exists.');
        $address = $m['local'] . '@' . $domain['domain'];
        if (strtolower(trim($confirm)) !== $address) {
            throw new ValidationException(['confirm' => 'Type the address to confirm.']);
        }
        try {
            $this->agent->call('mail.mailbox_delete', ['address' => $address, 'confirm' => $address], 60);
        } catch (AgentException $e) {
            if ($e->status() !== 404) {
                throw $e;
            }
        }
        $this->db->run('DELETE FROM mail_mailboxes WHERE id = ?', [$m['id']]);
        $this->audit->record('mail.mailbox_delete', 'mail_domain', (string) $domain['id'], AuditLogger::SUCCESS, ['address' => $address]);
    }

    /**
     * @param array<string, mixed> $domain
     * @param array<string, mixed> $input local ('*' = catch-all), destinations
     */
    public function saveAlias(array $domain, array $input): void
    {
        $local = strtolower(trim((string) ($input['alias_local'] ?? $input['local'] ?? '')));
        if ($local === '*') {
            $local = '';
        } elseif (!preg_match(self::LOCAL, $local) || str_contains($local, '..')) {
            throw new ValidationException(['alias_local' => 'Use letters, digits and . _ + -, or * for all other addresses.']);
        }
        $dests = array_values(array_unique(array_filter(array_map(static fn (string $d): string => strtolower(trim($d)), preg_split('/[\s,;]+/', (string) ($input['destinations'] ?? '')) ?: []))));
        if ($dests === [] || count($dests) > 50) {
            throw new ValidationException(['destinations' => 'Enter 1–50 addresses.']);
        }
        foreach ($dests as $d) {
            if (filter_var($d, FILTER_VALIDATE_EMAIL) === false) {
                throw new ValidationException(['destinations' => $d . ' is not an email address.']);
            }
        }
        if ($local !== '' && $this->db->scalar('SELECT COUNT(*) FROM mail_mailboxes WHERE domain_id = ? AND local = ?', [$domain['id'], $local]) > 0) {
            throw new ValidationException(['alias_local' => 'A mailbox has this address.']);
        }
        $source = ($local === '' ? '' : $local) . '@' . $domain['domain'];
        $this->agent->call('mail.alias_save', ['source' => $source, 'destinations' => $dests], 60);
        $this->db->run(
            'INSERT INTO mail_aliases (domain_id, local, destinations, created_at) VALUES (?, ?, ?, ?) ON CONFLICT (domain_id, local) DO UPDATE SET destinations = excluded.destinations',
            [$domain['id'], $local, implode(', ', $dests), gmdate('c')]
        );
        $this->audit->record('mail.alias_save', 'mail_domain', (string) $domain['id'], AuditLogger::SUCCESS, ['source' => $source, 'destinations' => $dests]);
    }

    /** @param array<string, mixed> $domain */
    public function deleteAlias(array $domain, int $aliasId): void
    {
        $a = $this->db->fetch('SELECT * FROM mail_aliases WHERE id = ? AND domain_id = ?', [$aliasId, $domain['id']]) ?? throw new UserFacingException('The alias no longer exists.');
        $source = $a['local'] . '@' . $domain['domain'];
        try {
            $this->agent->call('mail.alias_delete', ['source' => $source]);
        } catch (AgentException $e) {
            if ($e->status() !== 404) {
                throw $e;
            }
        }
        $this->db->run('DELETE FROM mail_aliases WHERE id = ?', [$a['id']]);
        $this->audit->record('mail.alias_delete', 'mail_domain', (string) $domain['id'], AuditLogger::SUCCESS, ['source' => $source]);
    }
}
