<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\MailFilterService;
use NimVor\Services\MailService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * REST API v1: a mailbox's filters and auto-responder, and a domain's mail routing. Every endpoint calls
 * MailFilterService and MailService, as the web interface does. The filter rules are checked here before
 * anything reaches the mail server (which checks them again), and a customer only reaches the mailboxes of
 * their own email domains.
 */
final class MailFiltersApiController
{
    use ApiHelpers;

    private const MAX_FILTERS = 50;
    private const MAX_PARTS = 20;

    public function __construct(
        private readonly MailService $mail,
        private readonly MailFilterService $filters,
    ) {
    }

    /**
     * @return array{0: array<string, mixed>, 1: array<string, mixed>}|null
     */
    private function target(Request $r, string $id, string $mailbox): ?array
    {
        $domain = $this->mail->domain((int) $id, $this->user($r));
        if ($domain === null) {
            return null;
        }
        try {
            return [$domain, $this->filters->mailbox($domain, (int) $mailbox)];
        } catch (UserFacingException) {
            return null;
        }
    }

    /**
     * @param array<string, mixed> $f
     * @return array<string, mixed>
     */
    private static function filterOut(int $n, array $f): array
    {
        return [
            'index' => $n, 'name' => (string) ($f['name'] ?? ''), 'enabled' => !empty($f['enabled']), 'match' => !empty($f['any']) ? 'any' : 'all',
            'rules' => array_values(array_map(static fn ($x): array => ['part' => (string) (((array) $x)['part'] ?? ''), 'match' => (string) (((array) $x)['match'] ?? ''), 'value' => (string) (((array) $x)['value'] ?? '')], (array) ($f['rules'] ?? []))),
            'actions' => array_values(array_map(static fn ($x): array => ['type' => (string) (((array) $x)['type'] ?? ''), 'value' => (string) (((array) $x)['value'] ?? '')], (array) ($f['actions'] ?? []))),
        ];
    }

    /**
     * @param array<string, mixed> $d
     * @return array<string, mixed>
     */
    private static function dataOut(array $d): array
    {
        $resp = (array) $d['responder'];
        return [
            'filters' => array_values(array_map(static fn (array $f, int $n): array => self::filterOut($n, $f), (array) $d['filters'], array_keys((array) $d['filters']))),
            'responder' => self::responderOut($resp),
        ];
    }

    /**
     * @param array<string, mixed> $r
     * @return array<string, mixed>
     */
    private static function responderOut(array $r): array
    {
        return ['enabled' => !empty($r['enabled']), 'subject' => (string) ($r['subject'] ?? ''), 'body' => (string) ($r['body'] ?? ''), 'days' => (int) ($r['days'] ?? 1), 'start' => (string) ($r['start'] ?? ''), 'end' => (string) ($r['end'] ?? '')];
    }

    /**
     * A filter as sent, checked: known parts, matches and actions, a value where one is needed.
     *
     * @param array<string, mixed> $in
     * @return array<string, mixed> in the form MailFilterService stores
     */
    private static function filterFrom(array $in): array
    {
        $errors = [];
        $name = trim((string) ($in['name'] ?? ''));
        if ($name === '' || mb_strlen($name) > 100) {
            $errors['name'] = 'Name the filter (up to 100 characters).';
        }
        $rules = [];
        foreach ((array) ($in['rules'] ?? []) as $i => $x) {
            $x = (array) $x;
            $part = (string) ($x['part'] ?? '');
            $match = (string) ($x['match'] ?? 'contains');
            $value = trim((string) ($x['value'] ?? ''));
            if (!isset(MailFilterService::PARTS[$part]) || !isset(MailFilterService::MATCHES[$match]) || $value === '' || mb_strlen($value) > 500) {
                $errors['rules'] = 'Rule ' . ($i + 1) . ': use a part (' . implode(', ', array_keys(MailFilterService::PARTS)) . '), a match (' . implode(', ', array_keys(MailFilterService::MATCHES)) . ') and a value of up to 500 characters.';
                break;
            }
            $rules[] = ['part' => $part, 'match' => $match, 'value' => $value];
        }
        if ($rules === [] && !isset($errors['rules'])) {
            $errors['rules'] = 'Add at least one condition.';
        }
        if (count($rules) > self::MAX_PARTS) {
            $errors['rules'] = 'At most ' . self::MAX_PARTS . ' conditions.';
        }
        $actions = [];
        foreach ((array) ($in['actions'] ?? []) as $i => $x) {
            $x = (array) $x;
            $type = (string) ($x['type'] ?? '');
            $value = trim((string) ($x['value'] ?? ''));
            $needsValue = in_array($type, ['fileinto', 'redirect', 'redirect_copy'], true);
            if (!isset(MailFilterService::ACTIONS[$type]) || ($needsValue && $value === '') || (str_starts_with($type, 'redirect') && filter_var($value, FILTER_VALIDATE_EMAIL) === false) || mb_strlen($value) > 255) {
                $errors['actions'] = 'Action ' . ($i + 1) . ': use ' . implode(', ', array_keys(MailFilterService::ACTIONS)) . '; a folder for fileinto, an address for redirect and redirect_copy.';
                break;
            }
            $actions[] = ['type' => $type, 'value' => $needsValue ? $value : ''];
        }
        if ($actions === [] && !isset($errors['actions'])) {
            $errors['actions'] = 'Add at least one action.';
        }
        if (count($actions) > self::MAX_PARTS) {
            $errors['actions'] = 'At most ' . self::MAX_PARTS . ' actions.';
        }
        $match = (string) ($in['match'] ?? 'all');
        if (!in_array($match, ['all', 'any'], true)) {
            $errors['match'] = 'Use all or any.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return MailFilterService::fromForm(['name' => $name, 'enabled' => $in['enabled'] ?? true, 'any' => $match === 'any' ? '1' : '0', 'rules' => $rules, 'actions' => $actions]);
    }

    public function show(Request $r, string $id, string $mailbox): Response
    {
        $t = $this->target($r, $id, $mailbox);
        if ($t === null) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok(self::dataOut($this->filters->get((string) $t[1]['address']))));
    }

    public function addFilter(Request $r, string $id, string $mailbox): Response
    {
        $t = $this->target($r, $id, $mailbox);
        if ($t === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $t): Response {
            $filter = self::filterFrom(self::body($r));
            $data = $this->filters->get((string) $t[1]['address']);
            if (count($data['filters']) >= self::MAX_FILTERS) {
                throw new UserFacingException('A mailbox can have at most ' . self::MAX_FILTERS . ' filters.');
            }
            $list = $data['filters'];
            $list[] = $filter;
            $this->filters->save((string) $t[1]['address'], $list, $data['responder'], 'filter saved');
            return self::ok(self::filterOut(count($list) - 1, $filter), 201);
        });
    }

    public function replaceFilter(Request $r, string $id, string $mailbox, string $n): Response
    {
        return $this->changeFilter($r, $id, $mailbox, $n, function (array $list, int $i) use ($r): array {
            $list[$i] = self::filterFrom(self::body($r));
            return [$list, 'filter saved'];
        });
    }

    public function patchFilter(Request $r, string $id, string $mailbox, string $n): Response
    {
        return $this->changeFilter($r, $id, $mailbox, $n, function (array $list, int $i) use ($r): array {
            $in = self::body($r);
            if (!array_key_exists('enabled', $in) || filter_var($in['enabled'], FILTER_VALIDATE_BOOL, FILTER_NULL_ON_FAILURE) === null) {
                throw new ValidationException(['enabled' => 'Send true or false (to change the rules, replace the filter with PUT).']);
            }
            $list[$i]['enabled'] = filter_var($in['enabled'], FILTER_VALIDATE_BOOL);
            return [$list, 'filter toggled'];
        });
    }

    public function deleteFilter(Request $r, string $id, string $mailbox, string $n): Response
    {
        $t = $this->target($r, $id, $mailbox);
        if ($t === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($t, $n): Response {
            $data = $this->filters->get((string) $t[1]['address']);
            if (!isset($data['filters'][(int) $n])) {
                return self::notFound();
            }
            $list = $data['filters'];
            array_splice($list, (int) $n, 1);
            $this->filters->save((string) $t[1]['address'], $list, $data['responder'], 'filter deleted');
            return self::ok(['deleted' => (int) $n]);
        });
    }

    /** @param callable(list<array<string, mixed>>, int): array{0: list<array<string, mixed>>, 1: string} $fn */
    private function changeFilter(Request $r, string $id, string $mailbox, string $n, callable $fn): Response
    {
        $t = $this->target($r, $id, $mailbox);
        if ($t === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($t, $n, $fn): Response {
            $data = $this->filters->get((string) $t[1]['address']);
            $i = (int) $n;
            if (!isset($data['filters'][$i])) {
                return self::notFound();
            }
            [$list, $what] = $fn($data['filters'], $i);
            $this->filters->save((string) $t[1]['address'], $list, $data['responder'], $what);
            return self::ok(self::filterOut($i, (array) $list[$i]));
        });
    }

    public function setResponder(Request $r, string $id, string $mailbox): Response
    {
        $t = $this->target($r, $id, $mailbox);
        if ($t === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $t): Response {
            $in = self::body($r);
            $errors = [];
            $enabled = filter_var($in['enabled'] ?? false, FILTER_VALIDATE_BOOL);
            $subject = trim((string) ($in['subject'] ?? ''));
            $body = (string) ($in['body'] ?? '');
            $days = $in['days'] ?? 1;
            $date = static fn (string $d): bool => $d === '' || (preg_match('/^\d{4}-\d{2}-\d{2}$/', $d) === 1 && checkdate((int) substr($d, 5, 2), (int) substr($d, 8, 2), (int) substr($d, 0, 4)));
            $start = trim((string) ($in['start'] ?? ''));
            $end = trim((string) ($in['end'] ?? ''));
            if ($enabled && ($subject === '' || trim($body) === '')) {
                $errors['subject'] = 'An auto-responder that is on needs a subject and a message.';
            }
            if (mb_strlen($subject) > 200 || strpbrk($subject, "\r\n") !== false) {
                $errors['subject'] = 'The subject is one line of up to 200 characters.';
            }
            if (strlen($body) > 10000) {
                $errors['body'] = 'The message can be up to 10000 characters.';
            }
            if (filter_var($days, FILTER_VALIDATE_INT) === false || (int) $days < 1 || (int) $days > 30) {
                $errors['days'] = 'Answer the same sender again after 1–30 days.';
            }
            if (!$date($start) || !$date($end) || ($start !== '' && $end !== '' && $end < $start)) {
                $errors['start'] = 'Use dates as YYYY-MM-DD, the end not before the start (or leave them empty).';
            }
            if ($errors !== []) {
                throw new ValidationException($errors);
            }
            $resp = MailFilterService::responderFromForm(['enabled' => $enabled ? '1' : '0', 'subject' => $subject, 'body' => $body, 'days' => (int) $days, 'start' => $start, 'end' => $end]);
            $data = $this->filters->get((string) $t[1]['address']);
            $this->filters->save((string) $t[1]['address'], $data['filters'], $resp, 'autoresponder');
            return self::ok(self::responderOut($resp));
        });
    }

    public function setRouting(Request $r, string $id): Response
    {
        $domain = $this->mail->domain((int) $id, $this->user($r));
        if ($domain === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $domain): Response {
            $routing = (string) (self::body($r)['routing'] ?? '');
            if (!isset(MailFilterService::ROUTING[$routing])) {
                throw new ValidationException(['routing' => 'Use local, backup or remote.']);
            }
            $this->filters->setRouting($domain, $routing);
            return self::ok(['id' => (int) $domain['id'], 'domain' => (string) $domain['domain'], 'routing' => $routing]);
        });
    }
}
