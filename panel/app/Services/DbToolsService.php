<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;

/**
 * phpMyAdmin and Adminer, installed by an administrator next to NimVor's own database browser. The agent installs,
 * serves and removes them; this service asks it which are there (so database pages can offer them) and passes on
 * the administrator's install and remove requests.
 */
final class DbToolsService
{
    public const TOOLS = ['phpmyadmin' => 'phpMyAdmin', 'adminer' => 'Adminer'];

    public function __construct(private readonly AgentClient $agent, private readonly AuditLogger $audit)
    {
    }

    /**
     * What the agent reports: tools (id, name, description, installed, path), php, panel_https. Null when the agent
     * cannot answer.
     *
     * @return array<string, mixed>|null
     */
    public function status(): ?array
    {
        try {
            return (array) $this->agent->call('dbtools.status');
        } catch (AgentException) {
            return null;
        }
    }

    /**
     * The installed tools for the "open in" links: id => [name, path]. Empty when none or the agent is silent.
     *
     * @return array<string, array{name: string, path: string}>
     */
    public function installed(): array
    {
        $out = [];
        foreach ((array) (($this->status() ?? [])['tools'] ?? []) as $t) {
            $t = (array) $t;
            if (!empty($t['installed']) && isset(self::TOOLS[(string) $t['id']])) {
                $out[(string) $t['id']] = ['name' => (string) $t['name'], 'path' => (string) $t['path']];
            }
        }
        return $out;
    }

    /** Start installing a tool; returns the job id. */
    public function install(string $tool, ?int $userId): string
    {
        $this->known($tool);
        $res = (array) $this->agent->call('dbtools.install', ['tool' => $tool]);
        $this->audit->record('dbtools.install', 'server', $tool, AuditLogger::SUCCESS, ['tool' => $tool]);
        return (string) ($res['job_id'] ?? '');
    }

    public function remove(string $tool): void
    {
        $this->known($tool);
        $this->agent->call('dbtools.remove', ['tool' => $tool], 120);
        $this->audit->record('dbtools.remove', 'server', $tool, AuditLogger::SUCCESS, ['tool' => $tool]);
    }

    private function known(string $tool): void
    {
        if (!isset(self::TOOLS[$tool])) {
            throw new \NimVor\Support\UserFacingException('Unknown database tool.');
        }
    }
}
