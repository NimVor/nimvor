<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Security\Permissions as P;
use WhaCP\Security\TwoFactor;
use WhaCP\Security\WebAuthn;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\ServerContext;
use WhaCP\Services\ServerSetupService;
use WhaCP\Services\SettingsService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/**
 * The setup wizard: choose a server profile and run its steps from the
 * browser (each step one request; a step that starts a job is followed in
 * its log and called again when the job ends), see what is done and what
 * is left, and the recommended next steps. Opened on the first visit of an
 * administrator to the dashboard; reopened from Server → Setup wizard.
 */
final class SetupController extends Controller
{
    /** Permissions running the setup needs: it acts on all of these. */
    public const RUN_PERMISSIONS = [P::SERVER_MANAGE, P::PACKAGES_MANAGE, P::PHP_MANAGE, P::SERVICES_MANAGE, P::SECURITY_MANAGE, P::MAIL_MANAGE, P::DNS_MANAGE, P::FTP_MANAGE, P::PLANS_MANAGE, P::BACKUPS_MANAGE, P::SETTINGS_MANAGE];

    /** PHP versions offered in the wizard. */
    public const PHP_CHOICES = ['8.0', '8.1', '8.2', '8.3', '8.4'];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly ServerSetupService $setup,
        private readonly SettingsService $settings,
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly TwoFactor $twoFactor,
        private readonly WebAuthn $webAuthn,
    ) {
        parent::__construct($view, $session);
    }

    private function mayRun(): bool
    {
        foreach (self::RUN_PERMISSIONS as $p) {
            if (!$this->view->can($p)) {
                return false;
            }
        }
        return true;
    }

    public function index(Request $request): Response
    {
        if (!$this->settings->bool('setup_wizard_seen')) {
            $this->settings->update(['setup_wizard_seen' => true], $this->userId($request));
        }
        $components = null;
        $memory = 0;
        $agentError = null;
        try {
            $components = (array) json_decode((string) json_encode($this->agent->call('components.status', [], 60)), true);
            $memory = (int) (((array) (((array) $this->agent->call('system.metrics'))['memory'] ?? []))['total_bytes'] ?? 0);
        } catch (AgentException $e) {
            $agentError = $e;
        }
        $last = $this->setup->last();
        $mb = intdiv($memory, 1048576);
        $recommended = $mb === 0 ? 'web' : ($mb >= 3800 ? 'full' : ($mb >= 1800 ? 'web' : 'minimal'));
        $profile = (string) ($last['profile'] ?? $recommended);
        $options = (array) ($last['options'] ?? []);
        $hostname = (string) ($options['hostname'] ?? '');
        if ($hostname === '') {
            $h = strtolower((string) gethostname());
            $hostname = str_contains($h, '.') ? $h : '';
        }
        $steps = [];
        foreach (ServerSetupService::PROFILES as $p => $_) {
            $steps[$p] = ServerSetupService::steps($p);
        }
        return $this->render('setup/index', [
            'last' => $last, 'profile' => $profile, 'recommended' => $recommended, 'memoryMb' => $mb,
            'hostname' => $hostname, 'php' => (array) ($options['php'] ?? ServerSetupService::PHP_DEFAULT),
            'thirdParty' => (bool) ($options['third_party_repos'] ?? true), 'steps' => $steps,
            'components' => $components, 'agentError' => $agentError, 'expected' => $this->expected($profile, $components ?? []),
            'checklist' => $this->checklist($request, $last, $components, $profile), 'mayRun' => $this->mayRun(),
            'done' => $this->settings->bool('setup_wizard_done'),
        ]);
    }

    /**
     * @param array<int, array<string, mixed>> $components
     * @return list<string>
     */
    private function expected(string $profile, array $components): array
    {
        $saved = $this->settings->string('components_expected');
        if ($saved !== '' && $saved !== 'none') {
            return explode(',', $saved);
        }
        return ServerSetupService::PROFILES[$profile] ?? [];
    }

    /**
     * What is recommended after the setup, done or not.
     *
     * @param array<string, mixed>|null $last
     * @param array<int, array<string, mixed>>|null $components
     * @return list<array{id: string, label: string, detail: string, done: bool, route: string}>
     */
    private function checklist(Request $request, ?array $last, ?array $components, string $profile): array
    {
        $uid = $this->userId($request);
        $failed = $last === null ? [] : array_keys(array_filter((array) ($last['steps'] ?? []), static fn (array $s): bool => ($s['status'] ?? '') === 'fail'));
        $items = [
            ['setup', 'Set up the server', $last === null ? 'Choose a profile and run the setup below.' : ($failed === [] ? 'The last setup finished without failures.' : count($failed) . ' step(s) failed in the last setup: run it again once fixed.'),
                $last !== null && $last['finished_at'] !== null && $failed === [], 'setup'],
        ];
        if ($components !== null) {
            $expected = $this->expected($profile, $components);
            $used = array_filter($components, static fn (array $c): bool => in_array($c['id'], $expected, true));
            $notReady = array_values(array_map(static fn (array $c): string => (string) $c['name'], array_filter($used, static fn (array $c): bool => empty($c['ready']))));
            $items[] = ['components', 'Every component ready', $notReady === [] ? 'Everything this server should have is installed, set up and running.' : 'Needs attention: ' . implode(', ', $notReady) . '.', $notReady === [], 'components'];
        }
        $items[] = ['two_factor', 'Two-factor sign-in for your account', 'A code from an app or a security key besides your password.', $this->twoFactor->enabled($uid) || $this->webAuthn->has($uid), 'profile'];
        $items[] = ['remote_backups', 'Backups kept off this server', 'Add a remote storage (S3, SFTP, FTP or Google Drive) and use it in the backup schedules.',
            (int) $this->db->scalar('SELECT COUNT(*) FROM backup_storages WHERE active = 1') > 0, 'backups'];
        $items[] = ['backup_key', 'Backup key downloaded', 'Encrypted backups cannot be restored on a new server without it.', (int) $this->db->scalar('SELECT COUNT(*) FROM audit_logs WHERE action = ? AND result = ?', ['backup.key_export', AuditLogger::SUCCESS]) > 0, 'backups'];
        $items[] = ['account', 'First hosting account', 'Create an account with a plan and its website.',
            (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ?', [$this->server->id()]) > 0, 'accounts.create'];
        return array_map(static fn (array $i): array => ['id' => $i[0], 'label' => $i[1], 'detail' => $i[2], 'done' => $i[3], 'route' => $i[4]], $items);
    }

    /** One setup step (JSON, from the wizard's script). */
    public function step(Request $request): Response
    {
        if (!$this->mayRun()) {
            return Response::json(['success' => false, 'error' => ['message' => 'Running the setup needs an administrator: it installs packages and changes the firewall, email, DNS, plans and backups.']], 403);
        }
        // Package installs are jobs; email or the firewall can take a while.
        set_time_limit(600);
        $this->setup->actorId = $this->userId($request);
        $this->setup->actorIp = $request->ip();
        $profile = $request->string('profile');
        $php = array_values(array_map('strval', (array) $request->input('php', [])));
        $options = ['hostname' => $request->string('hostname'), 'php' => $php, 'third_party_repos' => filter_var($request->input('third_party_repos', true), FILTER_VALIDATE_BOOL)];
        $after = $request->string('after_job');
        try {
            if (array_diff($php, self::PHP_CHOICES) !== []) {
                throw new ValidationException(['php' => 'Choose PHP versions from the list.']);
            }
            $res = $this->setup->runStep($profile, $options, $request->string('step'), $after === '' ? null : $after, max(0, (int) $request->input('round', 0)));
        } catch (ValidationException $e) {
            return Response::json(['success' => false, 'error' => ['message' => implode(' ', $e->errors())]], 422);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 503);
        } catch (UserFacingException $e) {
            return Response::json(['success' => false, 'error' => ['message' => $e->getMessage()]], 409);
        }
        if ($res['status'] === 'running' && $after === '') {
            $this->audit->record('server.setup_step', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['profile' => $profile, 'step' => $res['id'], 'job_id' => $res['job_id'] ?? null]);
        }
        return Response::json(['success' => true, 'step' => $res]);
    }

    /** Closes the wizard: the dashboard no longer offers it (Server → Setup wizard still does). */
    public function dismiss(Request $request): Response
    {
        $this->settings->update(['setup_wizard_seen' => true, 'setup_wizard_done' => true], $this->userId($request));
        $this->audit->record('server.setup_dismiss', 'server', (string) $this->server->id());
        return $this->redirectWithSuccess('/', 'The setup wizard is closed. Reopen it any time under Server → Setup wizard.');
    }
}
