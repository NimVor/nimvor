<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Email administration (server administrators): the mail queue, the mail server's status and settings, the
 * troubleshooter, mailbox permission repair and delivery reports. The agent talks to Postfix and Dovecot; every
 * change is audited here, so the web interface and the API leave the same record.
 */
final class MailAdminService
{
    public const RANGES = ['1h' => 3600, '24h' => 86400, '7d' => 604800, '30d' => 2592000];
    public const QUEUE_ACTIONS = ['delete', 'retry', 'requeue', 'hold', 'release', 'flush'];
    private const ID = '/^[0-9A-Za-z]{6,20}$/';

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array<string, mixed> */
    private static function arr(mixed $v): array
    {
        return (array) json_decode((string) json_encode($v), true);
    }

    /** @return array<string, mixed> total, counts per queue, messages */
    public function queue(): array
    {
        return (array) $this->agent->call('mail.queue', [], 60);
    }

    /** @return array<string, mixed> a queued message's headers */
    public function message(string $id): array
    {
        if (preg_match(self::ID, $id) !== 1) {
            throw new ValidationException(['id' => 'Not a queue ID.']);
        }
        return (array) (((array) $this->agent->call('mail.queue_message', ['id' => $id]))['headers'] ?? []);
    }

    /**
     * Act on queued messages. `flush` retries every deferred message and needs no IDs.
     *
     * @param list<string> $ids
     * @return array<string, mixed> the agent's answer
     */
    public function queueAction(string $action, array $ids): array
    {
        if (!in_array($action, self::QUEUE_ACTIONS, true)) {
            throw new ValidationException(['action' => 'Use delete, retry, requeue, hold, release or flush.']);
        }
        $ids = array_values(array_filter(array_map('strval', $ids), static fn (string $i): bool => preg_match(self::ID, $i) === 1));
        if ($action !== 'flush' && $ids === []) {
            throw new UserFacingException('Select messages first.');
        }
        $res = (array) $this->agent->call('mail.queue_action', ['action' => $action, 'ids' => $ids], 120);
        $this->audit->record('mail.queue_' . $action, 'server', 'mail', AuditLogger::SUCCESS, ['count' => count($ids)]);
        return $res + ['count' => count($ids)];
    }

    /** @return array<string, mixed> services, checks, current values and labels */
    public function serverStatus(): array
    {
        return self::arr($this->agent->call('mail.server_status', [], 60));
    }

    /** @return array<string, mixed> */
    public function logTail(int $lines = 100): array
    {
        return self::arr($this->agent->call('mail.log_tail', ['lines' => max(1, min(500, $lines))], 60));
    }

    /**
     * Change managed Postfix and Dovecot settings; the agent checks them (postfix check, doveconf) and puts the
     * previous values back when it refuses.
     *
     * @param array<string, string> $postfix
     * @param array<string, string> $dovecot
     */
    public function setSettings(array $postfix, array $dovecot): void
    {
        $this->agent->call('mail.settings_set', ['postfix' => (object) $postfix, 'dovecot' => (object) $dovecot], 120);
        $this->audit->record('mail.settings', 'server', 'mail', AuditLogger::SUCCESS, ['postfix' => $postfix, 'dovecot' => $dovecot]);
    }

    /** @return array<string, mixed> how mail for an address is delivered, step by step */
    public function trace(string $address): array
    {
        return self::arr($this->agent->call('mail.trace', ['address' => mb_substr(trim($address), 0, 254)], 60));
    }

    /** @return array<string, mixed> */
    public function repair(string $domain): array
    {
        $domain = strtolower(trim($domain));
        try {
            $res = (array) $this->agent->call('mail.repair_permissions', ['domain' => $domain], 600);
        } catch (UserFacingException $e) {
            $this->audit->record('mail.repair_permissions', 'server', $domain !== '' ? $domain : 'mail', AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            throw $e;
        }
        $this->audit->record('mail.repair_permissions', 'server', $domain !== '' ? $domain : 'mail', AuditLogger::SUCCESS, ['fixed' => (int) ($res['fixed'] ?? 0)]);
        return $res;
    }

    /**
     * Delivery report from the mail logs.
     *
     * @return array<string, mixed> report, source
     */
    public function report(string $range, string $address, string $status): array
    {
        $seconds = self::RANGES[$range] ?? 86400;
        $rep = (array) $this->agent->call('mail.report', [
            'since' => gmdate('c', time() - $seconds), 'until' => gmdate('c'),
            'address' => mb_substr(trim($address), 0, 254), 'status' => $status, 'limit' => 500,
        ], 120);
        return ['report' => self::arr($rep['report'] ?? []), 'source' => (string) ($rep['source'] ?? '')];
    }
}
