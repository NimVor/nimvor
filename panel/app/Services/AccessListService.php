<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Allow and deny lists of addresses, browsers (User-Agent) and URLs, for one website or for the whole server.
 * An allow entry always wins over a deny entry. The agent writes the Nginx rules (geo and map variables), checks
 * them with nginx -t and rolls back when Nginx refuses; every change is audited.
 */
final class AccessListService
{
    public const MAX_SITE = 200;
    public const MAX_GLOBAL = 1000;
    public const KINDS = ['ip' => 'Address', 'ua' => 'Browser', 'url' => 'URL'];
    public const ACTIONS = ['deny' => 'Deny', 'allow' => 'Allow'];
    /** What a refused request is answered with. */
    public const STATUSES = [403 => '403 Forbidden', 404 => '404 Not Found', 444 => 'Close the connection (no answer)'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * @param array<string, mixed>|null $site null for the server-wide list
     * @return array{rules: list<array<string, string>>, status: int}
     */
    public function read(?array $site): array
    {
        $res = $site === null
            ? (array) $this->agent->call('protect.access_global_status', [])
            : (array) $this->agent->call('protect.status', ['domain' => (string) $site['domain']]);
        $rules = [];
        foreach ((array) ($res['access'] ?? []) as $r) {
            $r = (array) $r;
            $rules[] = ['kind' => (string) ($r['kind'] ?? ''), 'action' => (string) ($r['action'] ?? ''), 'value' => (string) ($r['value'] ?? ''), 'note' => (string) ($r['note'] ?? '')];
        }
        return ['rules' => $rules, 'status' => (int) ($res['access_status'] ?? 403)];
    }

    /**
     * @param array<string, mixed>|null $site
     * @param array<string, mixed> $in kind, action, value, note
     */
    public function add(?array $site, array $in, int $userId): string
    {
        self::writable($site);
        $rule = self::rule($in);
        $cur = $this->read($site);
        $list = $cur['rules'];
        foreach ($list as $i => $r) {
            if ($r['kind'] === $rule['kind'] && $r['value'] === $rule['value']) {
                $list[$i] = $rule; // the same entry: its action and note change
                $this->send($site, $list, $cur['status'], 'access_list.change', ['kind' => $rule['kind'], 'value' => $rule['value'], 'action' => $rule['action']], $userId);
                return 'The entry was changed to ' . $rule['action'] . '.';
            }
        }
        $this->guardSize($site, count($list) + 1);
        $list[] = $rule;
        $this->send($site, $list, $cur['status'], 'access_list.add', ['kind' => $rule['kind'], 'value' => $rule['value'], 'action' => $rule['action']], $userId);
        return ($rule['action'] === 'allow' ? 'Allowed: ' : 'Denied: ') . $rule['value'] . '.';
    }

    /** @param array<string, mixed>|null $site */
    public function remove(?array $site, string $kind, string $value, int $userId): string
    {
        self::writable($site);
        $cur = $this->read($site);
        $list = array_values(array_filter($cur['rules'], static fn (array $r): bool => !($r['kind'] === $kind && $r['value'] === $value)));
        if (count($list) === count($cur['rules'])) {
            throw new UserFacingException('That entry does not exist.', status: 404);
        }
        $this->send($site, $list, $cur['status'], 'access_list.remove', ['kind' => $kind, 'value' => $value], $userId);
        return 'The entry was removed.';
    }

    /** @param array<string, mixed>|null $site */
    public function setStatus(?array $site, int $status, int $userId): string
    {
        self::writable($site);
        if (!isset(self::STATUSES[$status])) {
            throw new ValidationException(['status' => 'Choose 403, 404 or close the connection.']);
        }
        $cur = $this->read($site);
        $this->send($site, $cur['rules'], $status, 'access_list.status', ['status' => $status], $userId);
        return 'Refused visitors now get: ' . self::STATUSES[$status] . '.';
    }

    /**
     * Adds (merge) or replaces the list from text, one entry per line: "deny ip 203.0.113.0/24 # note".
     *
     * @param array<string, mixed>|null $site
     */
    public function import(?array $site, string $text, bool $replace, int $userId): string
    {
        self::writable($site);
        $incoming = self::parse($text);
        if ($incoming === []) {
            throw new ValidationException(['text' => 'There is no entry in the text.']);
        }
        $cur = $this->read($site);
        $list = $replace ? [] : $cur['rules'];
        $index = [];
        foreach ($list as $i => $r) {
            $index[$r['kind'] . "\0" . $r['value']] = $i;
        }
        $added = 0;
        foreach ($incoming as $rule) {
            $k = $rule['kind'] . "\0" . $rule['value'];
            if (isset($index[$k])) {
                $list[$index[$k]] = $rule;
                continue;
            }
            $index[$k] = count($list);
            $list[] = $rule;
            $added++;
        }
        $this->guardSize($site, count($list));
        $this->send($site, $list, $cur['status'], 'access_list.import', ['entries' => count($incoming), 'replace' => $replace], $userId);
        return ($replace ? 'The list was replaced by ' : 'Imported ') . count($incoming) . ' entr' . (count($incoming) === 1 ? 'y' : 'ies') . ($replace ? '.' : ' (' . $added . ' new).');
    }

    /** The list as text, in the format import reads. @param array<string, mixed>|null $site */
    public function export(?array $site): string
    {
        $out = '';
        foreach ($this->read($site)['rules'] as $r) {
            $out .= $r['action'] . ' ' . $r['kind'] . ' ' . $r['value'] . ($r['note'] !== '' ? '  # ' . $r['note'] : '') . "\n";
        }
        return $out;
    }

    /**
     * @return list<array<string, string>>
     */
    public static function parse(string $text): array
    {
        $out = [];
        $errors = [];
        foreach (preg_split('/\r\n|\n|\r/', $text) ?: [] as $n => $line) {
            $line = trim($line);
            if ($line === '' || $line[0] === '#') {
                continue;
            }
            if (preg_match('/^(allow|deny)\s+(ip|ua|url)\s+(.+?)(?:\s+#\s*(.*))?$/i', $line, $m) !== 1) {
                $errors['line ' . ($n + 1)] = 'Use: allow|deny ip|ua|url value  # note';
                continue;
            }
            try {
                $out[] = self::rule(['action' => strtolower($m[1]), 'kind' => strtolower($m[2]), 'value' => $m[3], 'note' => $m[4] ?? '']);
            } catch (ValidationException $e) {
                $errors['line ' . ($n + 1)] = implode(' ', $e->errors());
            }
            if (count($errors) >= 5) {
                break;
            }
        }
        if ($errors !== []) {
            $msgs = [];
            foreach ($errors as $k => $v) {
                $msgs[$k] = ucfirst($k) . ': ' . $v;
            }
            throw new ValidationException($msgs);
        }
        return $out;
    }

    /**
     * @param array<string, mixed> $in
     * @return array<string, string>
     */
    public static function rule(array $in): array
    {
        $errors = [];
        $kind = (string) ($in['kind'] ?? '');
        $action = (string) ($in['action'] ?? '');
        $value = trim((string) ($in['value'] ?? ''));
        $note = trim((string) ($in['note'] ?? ''));
        if (!isset(self::KINDS[$kind])) {
            $errors['kind'] = 'Choose address, browser or URL.';
        }
        if (!isset(self::ACTIONS[$action])) {
            $errors['action'] = 'Choose allow or deny.';
        }
        if ($note !== '' && (strlen($note) > 100 || preg_match('/[\x00-\x1f]/', $note) === 1)) {
            $errors['note'] = 'A note is at most 100 characters on one line.';
        }
        if ($errors === []) {
            $err = match ($kind) {
                'ip' => self::address($value) ? null : 'Use an address (203.0.113.7, 2001:db8::1), a network (10.0.0.0/8) or a range (10.0.0.1-10.0.0.99).',
                'ua' => preg_match('/^[\x20-\x7e]{1,100}$/', $value) === 1 && preg_match('/["$\\\\{};]/', $value) !== 1 ? null : 'Use 1 to 100 printable characters, without quotes, $, backslash, braces or semicolons.',
                default => preg_match('#^/[A-Za-z0-9._~/-]{0,198}$#', $value) === 1 && !str_contains($value, '..') && !str_contains($value, '//') ? null : 'Use a URL path starting with /, such as /wp-admin/ or /xmlrpc.php.',
            };
            if ($err !== null) {
                $errors['value'] = $err;
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['kind' => $kind, 'action' => $action, 'value' => $value, 'note' => $note];
    }

    private static function address(string $v): bool
    {
        if ($v === '' || strlen($v) > 100) {
            return false;
        }
        if (str_contains($v, '/')) {
            [$ip, $bits] = explode('/', $v, 2);
            $max = filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false ? 32 : (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) !== false ? 128 : 0);
            return $max > 0 && ctype_digit($bits) && (int) $bits <= $max;
        }
        if (str_contains($v, '-')) {
            [$a, $b] = array_map('trim', explode('-', $v, 2));
            $v4 = filter_var($a, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false && filter_var($b, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false;
            $v6 = filter_var($a, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) !== false && filter_var($b, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) !== false;
            return ($v4 || $v6) && strcmp((string) inet_pton($a), (string) inet_pton($b)) <= 0;
        }
        return filter_var($v, FILTER_VALIDATE_IP) !== false;
    }

    /**
     * @param array<string, mixed>|null $site
     * @param list<array<string, string>> $rules
     * @param array<string, mixed> $details
     */
    private function send(?array $site, array $rules, int $status, string $action, array $details, int $userId): void
    {
        if ($site === null) {
            $this->agent->call('protect.access_global', ['status' => $status, 'rules' => $rules], 300);
            $this->audit->record($action, 'server', 'global', AuditLogger::SUCCESS, ['scope' => 'server-wide'] + $details, $userId);
            return;
        }
        $this->agent->call('protect.access', ['domain' => (string) $site['domain'], 'status' => $status, 'rules' => $rules], 120);
        $this->audit->record($action, 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']] + $details, $userId);
    }

    /** @param array<string, mixed>|null $site */
    private function guardSize(?array $site, int $count): void
    {
        $max = $site === null ? self::MAX_GLOBAL : self::MAX_SITE;
        if ($count > $max) {
            throw new UserFacingException('The list can have at most ' . $max . ' entries.', status: 409);
        }
    }

    /** @param array<string, mixed>|null $site */
    private static function writable(?array $site): void
    {
        if ($site !== null && !empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended; the lists cannot be changed.', status: 409);
        }
    }
}
