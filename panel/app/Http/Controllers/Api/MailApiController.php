<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\MailService;
use NimVor\Services\SiteOverview;
use NimVor\Support\UserFacingException;

/**
 * REST API v1: email domains, mailboxes and aliases. Every endpoint calls MailService, as the web
 * interface does, so addresses are checked, plan limits (mailboxes, quota) apply, passwords meet the
 * server's policy, and a customer only reaches the domains of their own websites.
 */
final class MailApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly MailService $mail,
        private readonly AgentClient $agent,
        private readonly SiteOverview $overview,
    ) {
    }

    /**
     * @param array<string, mixed> $d
     * @return array<string, mixed>
     */
    private static function domainOut(array $d): array
    {
        return [
            'id' => (int) $d['id'],
            'domain' => $d['domain'],
            'website_id' => ($d['website_id'] ?? null) === null ? null : (int) $d['website_id'],
            'owner_id' => ($d['owner_id'] ?? null) === null ? null : (int) $d['owner_id'],
            'routing' => (string) ($d['routing'] ?? 'local'),
            'dkim_selector' => (string) $d['dkim_selector'],
            'mailboxes' => isset($d['mailboxes']) ? (int) $d['mailboxes'] : null,
            'aliases' => isset($d['aliases']) ? (int) $d['aliases'] : null,
            'created_at' => $d['created_at'],
        ];
    }

    /**
     * @param array<string, mixed> $m
     * @param array<string, mixed> $domain
     * @return array<string, mixed>
     */
    private static function mailboxOut(array $m, array $domain): array
    {
        return ['id' => (int) $m['id'], 'local' => $m['local'], 'address' => $m['local'] . '@' . $domain['domain'], 'quota_mb' => (int) $m['quota_mb'], 'created_at' => $m['created_at']];
    }

    /**
     * @param array<string, mixed> $a
     * @param array<string, mixed> $domain
     * @return array<string, mixed>
     */
    private static function aliasOut(array $a, array $domain): array
    {
        return [
            'id' => (int) $a['id'], 'local' => $a['local'], 'source' => $a['local'] . '@' . $domain['domain'], 'catch_all' => $a['local'] === '',
            'destinations' => array_values(array_filter(array_map('trim', explode(',', (string) $a['destinations'])))), 'created_at' => $a['created_at'],
        ];
    }

    /** @return array<string, mixed>|null */
    private function domain(Request $r, string $id): ?array
    {
        return $this->mail->domain((int) $id, $this->user($r));
    }

    /** @return array<string, mixed>|null */
    private function mailbox(int $domainId, int $id): ?array
    {
        foreach ($this->mail->mailboxes($domainId) as $m) {
            if ((int) $m['id'] === $id) {
                return $m;
            }
        }
        return null;
    }

    public function listDomains(Request $r): Response
    {
        return self::ok(array_map([self::class, 'domainOut'], $this->mail->domains($this->user($r))));
    }

    public function createDomain(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $id = $this->mail->addDomain(self::body($r), $this->user($r));
            return self::ok($this->domainDetail((array) $this->mail->domain($id, $this->user($r))), 201);
        });
    }

    public function showDomain(Request $r, string $id): Response
    {
        $d = $this->domain($r, $id);
        return $d === null ? self::notFound() : self::ok($this->domainDetail($d));
    }

    /**
     * The domain with the DNS records it needs (MX, SPF, DKIM, DMARC).
     *
     * @param array<string, mixed> $d
     * @return array<string, mixed>
     */
    private function domainDetail(array $d): array
    {
        $host = (string) (($this->mail->status() ?? [])['hostname'] ?? '');
        $d['mailboxes'] = count($this->mail->mailboxes((int) $d['id']));
        $d['aliases'] = count($this->mail->aliases((int) $d['id']));
        $records = array_map(static fn (array $x): array => ['name' => $x['name'], 'type' => $x['type'], 'value' => $x['value'], 'priority' => (int) $x['priority'], 'note' => $x['note']], $this->mail->dnsRecords($d, $host));
        return self::domainOut($d) + ['dns_records' => $records];
    }

    public function deleteDomain(Request $r, string $id): Response
    {
        $d = $this->domain($r, $id);
        if ($d === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d): Response {
            $this->mail->removeDomain($d, (string) $r->query('confirm', ''));
            return self::ok(['deleted' => (int) $d['id'], 'domain' => $d['domain']]);
        });
    }

    public function publishDns(Request $r, string $id): Response
    {
        $d = $this->domain($r, $id);
        if ($d === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d): Response {
            $n = $this->mail->publishDns($d, (string) (($this->mail->status() ?? [])['hostname'] ?? ''), $this->user($r));
            return self::ok(['added' => $n]);
        });
    }

    public function dnsCheck(Request $r, string $id): Response
    {
        $d = $this->domain($r, $id);
        if ($d === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($d): Response {
            $ip = $this->overview->serverIp();
            try {
                $res = (array) $this->agent->call('mail.auth_check', [
                    'domain' => (string) $d['domain'],
                    'hostname' => (string) (($this->mail->status() ?? [])['hostname'] ?? ''),
                    'ips' => $ip !== null ? [$ip] : [],
                    'dkim_selector' => (string) $d['dkim_selector'],
                    'dkim_txt' => (string) $d['dkim_txt'],
                    'routing' => (string) ($d['routing'] ?? 'local'),
                ], 60);
            } catch (AgentException $e) {
                throw $e;
            }
            return self::ok(['checks' => array_values((array) ($res['checks'] ?? [])), 'simulated' => !empty($res['simulated'])]);
        });
    }

    public function listMailboxes(Request $r, string $id): Response
    {
        $d = $this->domain($r, $id);
        return $d === null ? self::notFound() : self::ok(array_map(fn (array $m): array => self::mailboxOut($m, $d), $this->mail->mailboxes((int) $d['id'])));
    }

    public function createMailbox(Request $r, string $id): Response
    {
        $d = $this->domain($r, $id);
        if ($d === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d): Response {
            $in = self::body($r);
            $in['generate'] = filter_var($in['generate_password'] ?? false, FILTER_VALIDATE_BOOL);
            $before = array_column($this->mail->mailboxes((int) $d['id']), 'id');
            $password = $this->mail->saveMailbox($d, null, $in);
            $new = array_values(array_filter($this->mail->mailboxes((int) $d['id']), static fn (array $m): bool => !in_array($m['id'], $before, true)));
            return self::ok(['mailbox' => self::mailboxOut((array) $new[0], $d), 'password' => $password], 201);
        });
    }

    public function updateMailbox(Request $r, string $id, string $mailbox): Response
    {
        $d = $this->domain($r, $id);
        $m = $d === null ? null : $this->mailbox((int) $d['id'], (int) $mailbox);
        if ($d === null || $m === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d, $m): Response {
            $in = self::body($r);
            $in['quota_mb'] ??= (int) $m['quota_mb']; // only what is sent changes
            $in['generate'] = filter_var($in['generate_password'] ?? false, FILTER_VALIDATE_BOOL);
            $password = $this->mail->saveMailbox($d, (int) $m['id'], $in);
            return self::ok(['mailbox' => self::mailboxOut((array) $this->mailbox((int) $d['id'], (int) $m['id']), $d), 'password' => $password]);
        });
    }

    public function deleteMailbox(Request $r, string $id, string $mailbox): Response
    {
        $d = $this->domain($r, $id);
        $m = $d === null ? null : $this->mailbox((int) $d['id'], (int) $mailbox);
        if ($d === null || $m === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d, $m): Response {
            $this->mail->deleteMailbox($d, (int) $m['id'], (string) $r->query('confirm', ''));
            return self::ok(['deleted' => (int) $m['id'], 'address' => $m['local'] . '@' . $d['domain']]);
        });
    }

    public function listAliases(Request $r, string $id): Response
    {
        $d = $this->domain($r, $id);
        return $d === null ? self::notFound() : self::ok(array_map(fn (array $a): array => self::aliasOut($a, $d), $this->mail->aliases((int) $d['id'])));
    }

    public function saveAlias(Request $r, string $id): Response
    {
        $d = $this->domain($r, $id);
        if ($d === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d): Response {
            $in = self::body($r);
            $local = strtolower(trim((string) ($in['local'] ?? '')));
            $in['destinations'] = implode(',', array_map('strval', (array) ($in['destinations'] ?? [])));
            $this->mail->saveAlias($d, ['local' => $local] + $in);
            $key = $local === '*' ? '' : $local;
            foreach ($this->mail->aliases((int) $d['id']) as $a) {
                if ($a['local'] === $key) {
                    return self::ok(self::aliasOut($a, $d), 201);
                }
            }
            throw new UserFacingException('The alias was not saved.', status: 500);
        });
    }

    public function deleteAlias(Request $r, string $id, string $alias): Response
    {
        $d = $this->domain($r, $id);
        $found = null;
        if ($d !== null) {
            foreach ($this->mail->aliases((int) $d['id']) as $a) {
                if ((int) $a['id'] === (int) $alias) {
                    $found = $a;
                }
            }
        }
        if ($d === null || $found === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($d, $found): Response {
            $this->mail->deleteAlias($d, (int) $found['id']);
            return self::ok(['deleted' => (int) $found['id'], 'source' => $found['local'] . '@' . $d['domain']]);
        });
    }
}
