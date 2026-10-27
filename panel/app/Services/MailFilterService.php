<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Database\Connection;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Email filters and autoresponders of mailboxes, and mail routing of
 * domains. The agent keeps the filters (with the mailbox) and renders
 * them to Sieve; access to the domain is checked by the caller.
 */
final class MailFilterService
{
    public const PARTS = ['from' => 'From', 'to' => 'To', 'cc' => 'Cc', 'reply-to' => 'Reply-To', 'subject' => 'Subject', 'list-id' => 'List-Id', 'body' => 'Body'];
    public const MATCHES = ['contains' => 'contains', 'not-contains' => 'does not contain', 'is' => 'is', 'not-is' => 'is not', 'begins' => 'begins with', 'not-begins' => 'does not begin with', 'ends' => 'ends with', 'not-ends' => 'does not end with'];
    public const ACTIONS = ['fileinto' => 'Move to folder', 'redirect' => 'Forward to', 'redirect_copy' => 'Send a copy to', 'mark_read' => 'Mark as read', 'discard' => 'Delete', 'stop' => 'Stop processing more filters'];
    public const ROUTING = ['local' => 'Local mail exchanger', 'backup' => 'Backup mail exchanger', 'remote' => 'Remote mail exchanger'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * @param array<string, mixed> $domain
     * @return array<string, mixed> the mailbox row with its address
     */
    public function mailbox(array $domain, int $mailboxId): array
    {
        $m = $this->db->fetch('SELECT * FROM mail_mailboxes WHERE id = ? AND domain_id = ?', [$mailboxId, (int) $domain['id']]);
        if ($m === null) {
            throw new UserFacingException('The mailbox no longer exists.', status: 404);
        }
        $m['address'] = $m['local'] . '@' . $domain['domain'];
        return $m;
    }

    /** @return array{filters: list<array<string, mixed>>, responder: array<string, mixed>} */
    public function get(string $address): array
    {
        $res = (array) $this->agent->call('mail.filters_get', ['address' => $address]);
        return [
            'filters' => array_values(array_map(static fn ($f): array => json_decode((string) json_encode($f), true), (array) ($res['filters'] ?? []))),
            'responder' => (array) json_decode((string) json_encode($res['responder'] ?? []), true) + ['enabled' => false, 'subject' => '', 'body' => '', 'days' => 1, 'start' => '', 'end' => ''],
        ];
    }

    /**
     * A filter from the form: name, any, rules[i][part|match|value],
     * actions[i][type|value]; empty rows are ignored.
     *
     * @param array<string, mixed> $input
     * @return array<string, mixed>
     */
    public static function fromForm(array $input): array
    {
        $rules = [];
        foreach ((array) ($input['rules'] ?? []) as $r) {
            $r = (array) $r;
            if (trim((string) ($r['value'] ?? '')) !== '') {
                $rules[] = ['part' => (string) ($r['part'] ?? ''), 'match' => (string) ($r['match'] ?? ''), 'value' => trim((string) $r['value'])];
            }
        }
        $actions = [];
        foreach ((array) ($input['actions'] ?? []) as $a) {
            $a = (array) $a;
            $type = (string) ($a['type'] ?? '');
            if ($type === '') {
                continue;
            }
            $actions[] = ['type' => $type, 'value' => trim((string) ($a['value'] ?? ''))];
        }
        $errors = [];
        if (trim((string) ($input['name'] ?? '')) === '') {
            $errors['name'] = 'Name the filter.';
        }
        if ($rules === []) {
            $errors['rules'] = 'Add at least one condition.';
        }
        if ($actions === []) {
            $errors['actions'] = 'Add at least one action.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['name' => trim((string) $input['name']), 'enabled' => filter_var($input['enabled'] ?? true, FILTER_VALIDATE_BOOL), 'any' => ($input['any'] ?? '') === '1', 'rules' => $rules, 'actions' => $actions];
    }

    /**
     * @param list<array<string, mixed>> $filters
     * @param array<string, mixed> $responder
     */
    public function save(string $address, array $filters, array $responder, string $what): void
    {
        $this->agent->call('mail.filters_save', ['address' => $address, 'filters' => array_values($filters), 'responder' => (object) $responder]);
        $this->audit->record('mail.filters', 'mailbox', $address, AuditLogger::SUCCESS, ['change' => $what, 'filters' => count($filters), 'autoresponder' => !empty($responder['enabled'])]);
    }

    /**
     * @param array<string, mixed> $input
     * @return array<string, mixed>
     */
    public static function responderFromForm(array $input): array
    {
        return [
            'enabled' => ($input['enabled'] ?? '') === '1',
            'subject' => trim((string) ($input['subject'] ?? '')),
            'body' => (string) ($input['body'] ?? ''),
            'days' => max(1, min(30, (int) ($input['days'] ?? 1))),
            'start' => trim((string) ($input['start'] ?? '')),
            'end' => trim((string) ($input['end'] ?? '')),
        ];
    }

    /** @param array<string, mixed> $domain */
    public function setRouting(array $domain, string $routing): void
    {
        if (!isset(self::ROUTING[$routing])) {
            throw new UserFacingException('Choose local, backup or remote.');
        }
        $this->agent->call('mail.routing_set', ['domain' => $domain['domain'], 'routing' => $routing]);
        $this->db->run('UPDATE mail_domains SET routing = ? WHERE id = ?', [$routing, (int) $domain['id']]);
        $this->audit->record('mail.routing', 'mail_domain', (string) $domain['id'], AuditLogger::SUCCESS, ['domain' => $domain['domain'], 'routing' => $routing]);
    }
}
