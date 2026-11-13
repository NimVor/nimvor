<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;

/**
 * Security → Anti-intrusion (administrators): which service and website accounts have their program
 * starts logged (audit system) or are protected (the programs on the blacklist may not be run by them),
 * the blacklist itself, the log of what ran and the operation log. The agent does the work; every change
 * is audited.
 */
final class IntrusionService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array<string, mixed> */
    public function status(): array
    {
        return json_decode((string) json_encode($this->agent->call('intrusion.status')), true) ?: [];
    }

    /** @return array<string, mixed> */
    public function executions(string $user, int $limit = 200): array
    {
        $params = ['limit' => max(1, min(1000, $limit))];
        if ($user !== '') {
            $params['user'] = $user;
        }
        return json_decode((string) json_encode($this->agent->call('intrusion.executions', $params)), true) ?: [];
    }

    /**
     * The master switch and, per account, whether its program starts are logged and whether it is protected.
     *
     * @param list<string> $log accounts whose program starts are logged
     * @param list<string> $protect accounts that are protected
     * @param array<string, mixed> $actor
     */
    public function saveAccounts(bool $enabled, array $log, array $protect, array $actor): string
    {
        $this->admin($actor);
        $known = array_column((array) ($this->status()['accounts'] ?? []), 'name');
        $log = array_values(array_unique(array_map('strval', $log)));
        $protect = array_values(array_unique(array_map('strval', $protect)));
        if (($unknown = array_diff(array_merge($log, $protect), $known)) !== []) {
            throw new UserFacingException('Not an account that can be logged or protected: ' . implode(', ', array_slice($unknown, 0, 5)) . '.');
        }
        $accounts = [];
        foreach (array_unique(array_merge($log, $protect)) as $name) {
            $accounts[$name] = ['log' => in_array($name, $log, true), 'protect' => in_array($name, $protect, true)];
        }
        $this->agent->call('intrusion.configure', ['enabled' => $enabled, 'accounts' => (object) $accounts], 180);
        $this->record('intrusion.configure', ['enabled' => $enabled, 'log' => $log, 'protect' => $protect], $actor);
        return $enabled
            ? 'Anti-intrusion is on: ' . count($log) . ' account(s) logged, ' . count($protect) . ' protected.'
            : 'Anti-intrusion is off. Nothing is logged or blocked.';
    }

    /**
     * Adds programs (names or full paths) to the blacklist.
     *
     * @param list<string> $programs
     * @param array<string, mixed> $actor
     */
    public function addPrograms(array $programs, array $actor): string
    {
        $this->admin($actor);
        $programs = array_values(array_filter(array_map(static fn ($p): string => trim((string) $p), $programs), static fn (string $p): bool => $p !== ''));
        if ($programs === []) {
            throw new UserFacingException('Enter the name or the full path of a program.');
        }
        $current = array_column((array) ($this->status()['blacklist'] ?? []), 'path');
        $this->setList(array_values(array_unique(array_merge(array_map('strval', $current), $programs))), $actor);
        return count($programs) === 1 ? $programs[0] . ' is on the blacklist.' : count($programs) . ' programs added to the blacklist.';
    }

    /** @param array<string, mixed> $actor */
    public function removeProgram(string $path, array $actor): string
    {
        $this->admin($actor);
        $current = array_map('strval', array_column((array) ($this->status()['blacklist'] ?? []), 'path'));
        if (!in_array($path, $current, true)) {
            throw new UserFacingException('That program is not on the blacklist.');
        }
        $this->setList(array_values(array_diff($current, [$path])), $actor);
        return $path . ' is no longer on the blacklist.';
    }

    /**
     * @param list<string> $programs
     * @param array<string, mixed> $actor
     */
    private function setList(array $programs, array $actor): void
    {
        $this->agent->call('intrusion.blacklist', ['programs' => $programs], 180);
        $this->record('intrusion.blacklist', ['programs' => $programs], $actor);
    }

    /** @param array<string, mixed> $actor */
    private function admin(array $actor): void
    {
        if (($actor['role_name'] ?? '') !== 'admin') {
            throw new UserFacingException('Only administrators change anti-intrusion settings.', status: 403);
        }
    }

    /**
     * @param array<string, mixed> $details
     * @param array<string, mixed> $actor
     */
    private function record(string $action, array $details, array $actor): void
    {
        $this->audit->record($action, 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $details, (int) $actor['id']);
    }
}
