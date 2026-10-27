<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;

/**
 * Nginx service and server-wide tuning, through the agent. Tuning is a
 * managed conf.d drop-in validated with nginx -t and rolled back if invalid.
 */
final class NginxService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly SettingsService $settings,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array{installed: bool, running: bool, config_ok: bool, config_error: string, tuning: bool} */
    public function status(): array
    {
        $s = (array) $this->agent->call('nginx.status');
        return [
            'installed' => !empty($s['installed']),
            'running' => !empty($s['running']),
            'config_ok' => !empty($s['config_ok']),
            'config_error' => (string) ($s['config_error'] ?? ''),
            'tuning' => !empty($s['tuning']),
        ];
    }

    /** @return array{ok: bool, output: string} */
    public function test(): array
    {
        $r = (array) $this->agent->call('nginx.test');
        return ['ok' => !empty($r['ok']), 'output' => (string) ($r['output'] ?? '')];
    }

    public function reload(?int $userId): void
    {
        $this->agent->call('nginx.reload');
        $this->audit->record('nginx.reload', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, [], $userId);
    }

    /** The tuning values currently stored in settings. */
    public function tuning(): array
    {
        return [
            'client_max_body_mb' => $this->settings->int('nginx_client_max_body_mb'),
            'keepalive_timeout' => $this->settings->int('nginx_keepalive'),
            'client_body_timeout' => $this->settings->int('nginx_body_timeout'),
            'send_timeout' => $this->settings->int('nginx_send_timeout'),
            'gzip' => $this->settings->bool('nginx_gzip'),
            'hide_version' => $this->settings->bool('nginx_hide_version'),
        ];
    }

    /**
     * Validate and store the tuning, then apply it on the server. The agent
     * rolls back on the server if nginx -t fails; this throws in that case
     * and the stored settings are left unchanged.
     *
     * @param array<string, mixed> $input
     */
    public function applyTuning(array $input, ?int $userId): void
    {
        $this->settings->update([
            'nginx_client_max_body_mb' => (int) ($input['client_max_body_mb'] ?? 128),
            'nginx_keepalive' => (int) ($input['keepalive_timeout'] ?? 65),
            'nginx_body_timeout' => (int) ($input['client_body_timeout'] ?? 60),
            'nginx_send_timeout' => (int) ($input['send_timeout'] ?? 60),
            'nginx_gzip' => ($input['gzip'] ?? '') === '1',
            'nginx_hide_version' => ($input['hide_version'] ?? '') === '1',
        ], $userId);
        $this->agent->call('nginx.tuning_apply', $this->tuning(), 120);
        $this->audit->record('nginx.tuning', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $this->tuning(), $userId);
    }
}
