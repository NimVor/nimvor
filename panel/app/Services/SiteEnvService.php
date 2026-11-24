<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Repositories\SiteEnvRepository;
use NimVor\Support\ValidationException;

/**
 * A website's environment variables, edited as ".env" lines (KEY=value) by an administrator or by the
 * website's owner. They are stored encrypted, written by the agent into the website's PHP-FPM pool and given
 * to its background workers. Values are never logged or audited; only the names are.
 */
final class SiteEnvService
{
    public const MAX_VARS = 100;
    public const MAX_VALUE = 4096;
    private const RESERVED_PREFIXES = ['LD_', 'PHP', 'NIMVOR_', 'HTTP_', 'REQUEST_', 'SCRIPT_', 'SERVER_', 'REMOTE_', 'REDIRECT_', 'CONTENT_', 'FCGI_'];
    private const RESERVED = [
        'PATH', 'HOME', 'USER', 'LOGNAME', 'SHELL', 'TMPDIR', 'TMP', 'TEMP', 'IFS', 'DOCUMENT_ROOT', 'DOCUMENT_URI', 'GATEWAY_INTERFACE',
        'QUERY_STRING', 'PATH_INFO', 'PATH_TRANSLATED', 'AUTH_TYPE', 'HTTPS', 'ORIG_PATH_INFO', 'BASH_ENV', 'ENV',
    ];

    public function __construct(
        private readonly SiteEnvRepository $store,
        private readonly WebsiteService $websites,
        private readonly AuditLogger $audit,
        private readonly ?WorkerService $workers = null,
    ) {
    }

    /** @return array<string, string> */
    public function get(array $site): array
    {
        return $this->store->get((int) $site['id']);
    }

    /** The variables as the text the window shows. @param array<string, string> $vars */
    public static function toText(array $vars): string
    {
        $lines = [];
        foreach ($vars as $k => $v) {
            $plain = $v !== '' && trim($v) === $v && !in_array($v[0], ['"', "'"], true) && !str_contains($v, ' #');
            $lines[] = $k . '=' . ($plain || $v === '' ? $v : "'" . $v . "'");
        }
        return implode("\n", $lines);
    }

    /**
     * @return array<string, string>
     * @throws ValidationException
     */
    public static function parse(string $text): array
    {
        $vars = [];
        $n = 0;
        foreach (preg_split('/\R/', $text) ?: [] as $raw) {
            $n++;
            $line = trim($raw);
            if ($line === '' || $line[0] === '#') {
                continue;
            }
            if (str_starts_with($line, 'export ')) {
                $line = ltrim(substr($line, 7));
            }
            if (!str_contains($line, '=')) {
                throw new ValidationException(['env' => "Line {$n}: write it as NAME=value."]);
            }
            [$name, $value] = explode('=', $line, 2);
            $name = trim($name);
            self::checkName($name, "Line {$n}: ");
            $value = trim($value);
            $len = strlen($value);
            if ($len >= 2 && (($value[0] === '"' && $value[$len - 1] === '"') || ($value[0] === "'" && $value[$len - 1] === "'"))) {
                $value = substr($value, 1, -1);
            }
            self::checkValue($name, $value, "Line {$n}: ");
            if (isset($vars[$name])) {
                throw new ValidationException(['env' => "Line {$n}: {$name} is set twice."]);
            }
            $vars[$name] = $value;
        }
        if (count($vars) > self::MAX_VARS) {
            throw new ValidationException(['env' => 'At most ' . self::MAX_VARS . ' variables.']);
        }
        return $vars;
    }

    /** @throws ValidationException */
    private static function checkName(string $name, string $where): void
    {
        if (preg_match('/^[A-Z][A-Z0-9_]{0,63}$/', $name) !== 1) {
            throw new ValidationException(['env' => "{$where}“{$name}” is not a valid name. Start with a capital letter and use capital letters, digits and _ (at most 64 characters)."]);
        }
        if (in_array($name, self::RESERVED, true)) {
            throw new ValidationException(['env' => "{$where}{$name} is reserved by the system."]);
        }
        foreach (self::RESERVED_PREFIXES as $p) {
            if (str_starts_with($name, $p)) {
                throw new ValidationException(['env' => "{$where}names starting with {$p} are reserved by the system."]);
            }
        }
    }

    /** @throws ValidationException */
    private static function checkValue(string $name, string $value, string $where): void
    {
        if (str_contains($value, "'")) {
            throw new ValidationException(['env' => "{$where}{$name}: a value may not contain a single quote (’)."]);
        }
        if (preg_match('/[\x00-\x1f\x7f]/', $value) === 1) {
            throw new ValidationException(['env' => "{$where}{$name}: a value may not contain control characters."]);
        }
        if (strlen($value) > self::MAX_VALUE) {
            throw new ValidationException(['env' => "{$where}{$name}: the value is longer than " . self::MAX_VALUE . ' characters.']);
        }
    }

    /**
     * Stores and applies the variables. When the agent refuses them the previous set is put back.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $actor
     * @return string a short note for the user (workers restarted, ...)
     */
    public function save(array $site, string $text, array $actor): string
    {
        return $this->saveVars($site, self::parse($text), $actor);
    }

    /**
     * Change some variables and leave the others as they are: `$set` adds or replaces, `$unset` removes. The
     * result is checked as a whole (names, value length, at most 100 variables) before anything is stored.
     *
     * @param array<string, mixed> $site
     * @param array<string, string> $set
     * @param list<string> $unset
     * @param array<string, mixed> $actor
     */
    public function change(array $site, array $set, array $unset, array $actor): string
    {
        $vars = $this->store->get((int) $site['id']);
        foreach ($unset as $name) {
            unset($vars[$name]);
        }
        foreach ($set as $name => $value) {
            $name = (string) $name;
            self::checkName($name, '');
            self::checkValue($name, (string) $value, '');
            $vars[$name] = (string) $value;
        }
        if (count($vars) > self::MAX_VARS) {
            throw new ValidationException(['env' => 'At most ' . self::MAX_VARS . ' variables.']);
        }
        return $this->saveVars($site, $vars, $actor);
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, string> $vars
     * @param array<string, mixed> $actor
     */
    private function saveVars(array $site, array $vars, array $actor): string
    {
        $id = (int) $site['id'];
        $before = $this->store->get($id);
        if ($before === $vars) {
            return 'Nothing changed.';
        }
        $this->store->set($id, $vars);
        try {
            $this->websites->applyState($site);
        } catch (\Throwable $e) {
            $this->store->set($id, $before);
            throw $e;
        }
        $note = '';
        if ($this->workers !== null) {
            $failed = 0;
            foreach ($this->workers->forSite($site) as $worker) {
                try {
                    $this->workers->apply($site, $worker);
                } catch (\Throwable) {
                    $failed++;
                }
            }
            $note = $failed > 0 ? " {$failed} background worker(s) could not be restarted; restart them on the Workers page." : '';
        }
        $this->audit->record('website.env', 'website', (string) $id, AuditLogger::SUCCESS, [
            'domain' => $site['domain'],
            'added' => array_values(array_diff(array_keys($vars), array_keys($before))),
            'removed' => array_values(array_diff(array_keys($before), array_keys($vars))),
            'changed' => array_values(array_keys(array_filter($vars, static fn (string $v, string $k): bool => isset($before[$k]) && $before[$k] !== $v, ARRAY_FILTER_USE_BOTH))),
        ], (int) ($actor['id'] ?? 0) ?: null);
        return 'Environment saved and applied.' . $note;
    }
}
