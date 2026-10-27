<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Security\Permissions as P;
use NimVor\Services\AuditLogger;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Services\SettingsService;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Server → Components: every part of the server with each step it needs
 * (installed, set up, running, started at boot, ports open) and a Fix per
 * step. The fixes are the existing actions (Packages' install queue,
 * services.control, the firewall, each component's own page); the steps
 * to act on are read from the agent again, never taken from the form.
 */
final class ComponentsController extends Controller
{
    /** Where each component's own settings are. */
    public const PAGES = [
        'web' => 'websites', 'php' => 'php', 'database' => 'databases.server', 'mail' => 'mail', 'dns' => 'dns',
        'ftp' => 'ftp', 'redis' => 'redis', 'memcached' => 'memcached', 'mongodb' => 'mongo', 'firewall' => 'security.firewall',
        'fail2ban' => 'security.fail2ban', 'clamav' => 'security.malware', 'certbot' => 'ssl.autossl',
    ];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
        private readonly SettingsService $settings,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $components = null;
        $error = null;
        try {
            $components = (array) json_decode((string) json_encode($this->agent->call('components.status', [], 60)), true);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('components/index', ['components' => $components, 'agentError' => $error, 'expected' => $this->expected($components ?? [])]);
    }

    /**
     * The components this server should have (the server profile). Until
     * an administrator chooses, the installed ones.
     *
     * @param array<int, array<string, mixed>> $components
     * @return list<string>
     */
    private function expected(array $components): array
    {
        $saved = $this->settings->string('components_expected');
        if ($saved !== '') {
            return array_values(array_filter(explode(',', $saved), static fn (string $id): bool => isset(self::PAGES[$id])));
        }
        return array_values(array_map(static fn (array $c): string => (string) $c['id'], array_filter($components, static fn (array $c): bool => !empty($c['installed']))));
    }

    /** Saves which components this server should have. */
    public function profile(Request $request): Response
    {
        $ids = array_values(array_intersect(array_keys(self::PAGES), array_map('strval', (array) $request->input('expected', []))));
        try {
            $this->settings->update(['components_expected' => $ids === [] ? 'none' : implode(',', $ids)], $this->userId($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError('/components', implode(' ', $e->errors()));
        }
        $this->audit->record('components.profile', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['expected' => $ids]);
        return $this->redirectWithSuccess('/components', 'Saved what this server should have.');
    }

    /**
     * Fixes one step of a component: install (missing packages), start,
     * boot (enable), ports (open in the firewall).
     */
    public function fix(Request $request, string $id, string $step): Response
    {
        $need = ['install' => P::PACKAGES_MANAGE, 'start' => P::SERVICES_MANAGE, 'boot' => P::SERVICES_MANAGE, 'ports' => P::SECURITY_MANAGE][$step] ?? null;
        if ($need === null || !isset(self::PAGES[$id])) {
            return $this->redirectWithError('/components', 'Unknown component or step.');
        }
        if (!$this->view->can($need)) {
            return $this->redirectWithError('/components', 'You may not do this.');
        }
        $back = '/components#c-' . $id;
        try {
            $c = null;
            foreach ((array) json_decode((string) json_encode($this->agent->call('components.status', [], 60)), true) as $row) {
                if (($row['id'] ?? '') === $id) {
                    $c = $row;
                }
            }
            if ($c === null) {
                return $this->redirectWithError('/components', 'Unknown component.');
            }
            $items = [];
            foreach ((array) $c['steps'] as $s) {
                if ($s['id'] === ['install' => 'installed', 'start' => 'running', 'boot' => 'boot', 'ports' => 'ports'][$step] && $s['status'] === 'todo') {
                    $items = array_map('strval', (array) ($s['items'] ?? []));
                }
            }
            if ($items === []) {
                return $this->redirectWithSuccess($back, 'Nothing to do: ' . $c['name'] . ' is already fine for this step.');
            }
            switch ($step) {
                case 'install':
                    $job = (string) ((array) $this->agent->call('packages.install', ['names' => $items]))['job_id'];
                    $this->jobs->track($job, 'Install ' . $c['name'], 'server', (string) $this->server->id(), $this->userId($request));
                    $this->audit->record('packages.install', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['names' => $items]);
                    $this->flashJob($job);
                    return $this->redirectWithSuccess($back, 'Installing ' . implode(', ', $items) . '…');
                case 'start':
                case 'boot':
                    $op = $step === 'start' ? 'start' : 'enable';
                    foreach ($items as $unit) {
                        $this->agent->call('services.control', ['unit' => $unit, 'op' => $op]);
                        $this->audit->record('service.' . $op, 'service', $unit);
                    }
                    return $this->redirectWithSuccess($back, ($op === 'start' ? 'Started: ' : 'Now started at boot: ') . implode(', ', $items) . '.');
                default: // ports
                    $ports = array_map(static function (string $p): array {
                        [$port, $proto] = explode('/', $p, 2) + [1 => 'tcp'];
                        return ['port' => $port, 'proto' => $proto];
                    }, $items);
                    $comment = substr(trim((string) preg_replace(['/[^A-Za-z0-9 ._:\/-]+/', '/ {2,}/'], ' ', (string) $c['name'])), 0, 60);
                    $res = (array) $this->agent->call('firewall.allow_ports', ['ports' => $ports, 'comment' => $comment], 90);
                    $this->audit->record('firewall.allow_ports', 'server', 'security', AuditLogger::SUCCESS, ['ports' => $items, 'pending' => ((array) ($res['pending'] ?? []))['id'] ?? null]);
                    return $this->redirectWithSuccess($back, empty($res['changed'])
                        ? 'Those ports are already allowed.'
                        : 'Opened ' . implode(', ', $items) . '. Check that you can still connect, then click Keep change within 2 minutes — otherwise it is undone automatically.');
            }
        } catch (AgentException $e) {
            return $this->redirectWithFailure($back, $e);
        }
    }
}
