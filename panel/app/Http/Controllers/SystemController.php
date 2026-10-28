<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\AuditLogger;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/**
 * Packages and server settings (host name, time zone, restart).
 */
final class SystemController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
        private readonly \NimVor\Repositories\UserRepository $users,
        private readonly \NimVor\Security\PasswordHasher $hasher,
        private readonly \NimVor\Security\PasswordPolicy $policy,
        private readonly \NimVor\Services\SiteOverview $overview,
    ) {
        parent::__construct($view, $session);
    }

    public function packages(Request $request): Response
    {
        if ($request->wantsJson()) {
            // The components with their state, for the installation queue.
            try {
                return Response::json(['success' => true, 'packages' => (array) $this->agent->call('packages.list')]);
            } catch (AgentException $e) {
                return Response::json(['success' => false, 'error' => $e->toArray()], 503);
            }
        }
        $data = ['packages' => [], 'updates' => null, 'health' => null, 'agentError' => null];
        try {
            $data['packages'] = (array) $this->agent->call('packages.list');
            $data['updates'] = (array) $this->agent->call('packages.updates', [], 150);
        } catch (AgentException $e) {
            $data['agentError'] = $e;
        }
        try {
            // Broken package state (agents before this check lack the action).
            $data['health'] = (array) $this->agent->call('packages.health', [], 150);
        } catch (AgentException) {
        }
        return $this->render('server/packages', $data);
    }

    /** @param callable(): (string|array{0: string, 1: string}) $fn */
    private function act(string $to, callable $fn): Response
    {
        try {
            $res = $fn();
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        if (is_array($res)) {
            $this->flashJob($res[1]);
            $res = $res[0];
        }
        return $this->redirectWithSuccess($to, $res);
    }

    private function job(Request $request, string $action, array $params, string $label): string
    {
        $job = (string) ((array) $this->agent->call($action, $params))['job_id'];
        $this->jobs->track($job, $label, 'server', (string) $this->server->id(), $this->userId($request));
        $this->audit->record($action, 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $params);
        return $job;
    }

    public function install(Request $request): Response
    {
        if ($request->wantsJson()) {
            // The installation queue: the job starts at once; the agent runs
            // component installs one after another.
            $name = $request->string('name');
            try {
                return Response::json(['success' => true, 'name' => $name, 'job_id' => $this->job($request, 'packages.install', ['name' => $name], 'Install ' . $name)]);
            } catch (AgentException $e) {
                return Response::json(['success' => false, 'error' => $e->toArray()], in_array($e->errorCode(), ['invalid_params', 'conflict'], true) ? 422 : 503);
            }
        }
        return $this->act('/packages', function () use ($request): array {
            $name = $request->string('name');
            return ['Installing ' . $name . '…', $this->job($request, 'packages.install', ['name' => $name], 'Install ' . $name)];
        });
    }

    public function refresh(Request $request): Response
    {
        return $this->act('/packages', fn (): array => ['Refreshing the package index…', $this->job($request, 'packages.refresh', [], 'Refresh package index')]);
    }

    public function upgrade(Request $request): Response
    {
        return $this->act('/packages', fn (): array => ['Installing updates…', $this->job($request, 'packages.upgrade', [], 'Install updates')]);
    }

    /**
     * Repairs broken package state. Removing duplicate versions removes
     * packages, so it needs its own confirmation (remove_duplicates).
     */
    public function repair(Request $request): Response
    {
        $dups = $request->input('remove_duplicates') === '1';
        return $this->act('/packages', fn (): array => [
            $dups ? 'Removing duplicate packages and repairing…' : 'Repairing packages…',
            $this->job($request, 'packages.repair', ['remove_duplicates' => $dups], $dups ? 'Remove duplicate packages' : 'Repair packages'),
        ]);
    }

    public function hostname(Request $request): Response
    {
        return $this->act('/server#settings', function () use ($request): string {
            $h = $request->string('hostname');
            $ip = $this->overview->serverIp();
            $this->agent->call('server.set_hostname', array_filter(['hostname' => $h, 'ip' => $ip]));
            $this->audit->record('server.hostname', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['hostname' => $h, 'ip' => $ip]);

            // Keep the mail server's own host name (HELO, greeting, the
            // certificate name) in step, so the change does not have to be
            // repeated by hand in Postfix and Dovecot.
            $mailMsg = '';
            try {
                $mail = (array) $this->agent->call('mail.status');
                if (!empty($mail['configured'])) {
                    $this->agent->call('mail.configure', ['hostname' => $h, 'confirm_edit_package_config' => true], 180);
                    $this->audit->record('mail.configure', 'server', 'mail', AuditLogger::SUCCESS, ['hostname' => $h, 'reason' => 'hostname change']);
                    $mailMsg = ' The mail server was reconfigured to use it.';
                }
            } catch (AgentException) {
                $mailMsg = ' The mail server could not be updated automatically — reapply it under Email if you host mail.';
            }

            return 'Host name changed to ' . $h . '.' . ($ip !== null ? ' /etc/hosts now resolves it to ' . $ip . '.' : '') . $mailMsg;
        });
    }

    /**
     * Find where a previous host name still appears in the managed
     * configuration (read-only). A host-name change updates /etc/hosts and
     * the mail server, but artefacts created earlier — DNS zone records,
     * Nginx server names — keep the old name until edited; this lists them.
     */
    public function hostnameReferences(Request $request): Response
    {
        $old = strtolower(trim((string) $request->query('old', '')));
        $report = null;
        $error = null;
        $webmail = [];
        if ($old !== '') {
            try {
                $report = (array) $this->agent->call('server.hostname_references', ['old' => $old], 60);
                // Webmail keeps its own host name and certificate from when it
                // was turned on; flag it when it still answers on the old name.
                $mail = (array) $this->agent->call('mail.status');
                $webmail = (array) ($mail['webmail'] ?? []);
            } catch (AgentException $e) {
                $error = $e->getMessage();
            }
        }
        return $this->render('server/hostname_references', [
            'old' => $old,
            'report' => $report,
            'error' => $error,
            'webmailStale' => !empty($webmail['enabled']) && strtolower((string) ($webmail['hostname'] ?? '')) === $old,
            'webmailHostname' => (string) ($webmail['hostname'] ?? ''),
        ]);
    }

    /** Re-apply webmail so it answers on, and gets its cert for, the current host name. */
    public function hostnameWebmail(Request $request): Response
    {
        return $this->act('/server/hostname-references', function () use ($request): string {
            $sys = (array) $this->agent->call('system.info');
            $h = strtolower((string) (((array) ($sys['system'] ?? []))['hostname'] ?? ''));
            if ($h === '') {
                throw new UserFacingException('The server host name could not be read.');
            }
            $webmail = (array) (((array) $this->agent->call('mail.status'))['webmail'] ?? []);
            if (empty($webmail['enabled'])) {
                return 'Webmail is not turned on; nothing to re-apply.';
            }
            $this->agent->call('mail.webmail_configure', ['enabled' => true, 'hostname' => $h, 'php_version' => (string) ($webmail['php'] ?? '')], 180);
            $this->audit->record('mail.webmail_configure', 'server', 'mail', AuditLogger::SUCCESS, ['hostname' => $h, 'reason' => 'hostname references cleanup']);
            return 'Webmail now answers on ' . $h . ' and uses its Let\'s Encrypt certificate when one exists for that name.';
        });
    }

    /** Re-apply the mail server so Postfix/Dovecot use the current host name. */
    public function hostnameRemail(Request $request): Response
    {
        return $this->act('/server/hostname-references', function () use ($request): string {
            $sys = (array) $this->agent->call('system.info');
            $h = strtolower((string) (((array) ($sys['system'] ?? []))['hostname'] ?? ''));
            if ($h === '') {
                throw new UserFacingException('The server host name could not be read.');
            }
            $mail = (array) $this->agent->call('mail.status');
            if (empty($mail['configured'])) {
                return 'Email is not set up on this server; nothing to re-apply.';
            }
            $this->agent->call('mail.configure', ['hostname' => $h, 'confirm_edit_package_config' => true], 180);
            $this->audit->record('mail.configure', 'server', 'mail', AuditLogger::SUCCESS, ['hostname' => $h, 'reason' => 'hostname references cleanup']);
            return 'The mail server was re-applied with the host name ' . $h . '. Re-publish each mail domain\'s DNS so its records point to it.';
        });
    }

    /**
     * The root password: administrators only, confirmed with their own
     * panel password. The password is never logged, stored or returned.
     */
    public function rootPassword(Request $request): Response
    {
        $user = $this->user($request);
        if (($user['role_name'] ?? '') !== 'admin') {
            return $this->redirectWithError('/server', 'Only administrators can change the root password.');
        }
        $current = (string) $request->input('current_password', '');
        $new = (string) $request->input('root_password', '');
        $confirm = (string) $request->input('root_password_confirmation', '');
        $hash = $this->users->passwordHash((int) $user['id']);
        if ($hash === null || !$this->hasher->verify($current, $hash)) {
            $this->audit->record('server.root_password', 'server', (string) $this->server->id(), AuditLogger::DENIED, ['reason' => 'wrong panel password']);
            return $this->redirectWithError('/server#root-password', 'Your panel password is not correct.');
        }
        if ($new !== $confirm) {
            return $this->redirectWithError('/server#root-password', 'The new passwords do not match.');
        }
        $problem = $this->policy->check($new, 'root');
        $classes = (int) preg_match('/[a-z]/', $new) + (int) preg_match('/[A-Z]/', $new) + (int) preg_match('/[0-9]/', $new) + (int) preg_match('/[^A-Za-z0-9]/', $new);
        if ($problem === null && (mb_strlen($new) < 14 || $classes < 3)) {
            $problem = 'Use at least 14 characters with three of: lower case, upper case, digits, symbols.';
        }
        if ($problem === null && (str_contains($new, ':') || preg_match('/[\x00-\x1f]/', $new))) {
            $problem = 'The password must not contain colons or control characters.';
        }
        if ($problem !== null) {
            return $this->redirectWithError('/server#root-password', $problem);
        }
        try {
            $this->agent->call('server.root_password', ['password' => $new]);
        } catch (\NimVor\Support\UserFacingException $e) {
            $this->audit->record('server.root_password', 'server', (string) $this->server->id(), AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/server#root-password', $e);
        }
        $this->audit->record('server.root_password', 'server', (string) $this->server->id());
        return $this->redirectWithSuccess('/server#settings', 'The root password was changed.');
    }

    public function timezone(Request $request): Response
    {
        return $this->act('/server#settings', function () use ($request): string {
            $tz = $request->string('timezone');
            $this->agent->call('server.set_timezone', ['timezone' => $tz]);
            $this->audit->record('server.timezone', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['timezone' => $tz]);
            return 'Time zone changed. Cron jobs and backup schedules now use ' . $tz . '.';
        });
    }

    /** The graphical login screen (GDM and the like): off or on again. */
    public function graphical(Request $request): Response
    {
        return $this->act('/server#settings', function () use ($request): string {
            $on = $request->input('enabled') === '1';
            $this->agent->call('server.graphical_set', ['enabled' => $on], 60);
            $this->audit->record('server.graphical', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['enabled' => $on]);
            return $on ? 'The graphical login screen is on again.' : 'The graphical login screen is off; the server boots to the text console.';
        });
    }

    public function reboot(Request $request): Response
    {
        return $this->act('/server#settings', function () use ($request): string {
            $minutes = max(1, (int) $request->input('minutes', 1));
            $this->agent->call('server.reboot', ['confirm' => $request->string('confirm'), 'minutes' => $minutes]);
            $this->audit->record('server.reboot', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['minutes' => $minutes]);
            return 'The server restarts in ' . $minutes . ' minute(s). Websites are unavailable for a short time.';
        });
    }

    public function cancelReboot(Request $request): Response
    {
        return $this->act('/server#settings', function (): string {
            $this->agent->call('server.reboot_cancel');
            $this->audit->record('server.reboot_cancel', 'server', (string) $this->server->id());
            return 'Restart cancelled.';
        });
    }
}
