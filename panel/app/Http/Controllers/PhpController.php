<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\JobTracker;
use WhaCP\View\View;

/**
 * PHP version management: install, enable/disable, list modules.
 */
final class PhpController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly JobTracker $jobs,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    /**
     * Adds the third-party repository with more PHP versions (Ubuntu:
     * Ondřej Surý's PPA with its pinned key; RHEL family: Remi).
     */
    public function enableRepository(Request $request): Response
    {
        if (!$this->view->can(\WhaCP\Security\Permissions::PHP_MANAGE)) {
            return $this->redirectWithError('/php', 'You cannot manage PHP versions.');
        }
        if ($request->string('confirm') !== 'yes') {
            return $this->redirectWithError('/php', 'Confirm that the repository should be added.');
        }
        try {
            $res = (array) $this->agent->call('php.repository.enable', []);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/php', $e);
        }
        $name = (string) ($res['repository'] ?? 'PHP repository');
        $this->jobs->track((string) $res['job_id'], 'Add ' . $name, 'php', 'repository', $this->userId($request));
        $this->audit->record('php.repository.enable', 'php', $name, AuditLogger::SUCCESS);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess('/php', 'Adding ' . $name . '. Its PHP versions appear in the list once the package index is refreshed.');
    }

    public function install(Request $request): Response
    {
        $version = $request->string('version');
        if (!preg_match('/^[5-9]\.[0-9]{1,2}$/', $version)) {
            return $this->redirectWithError('/php', 'Choose a PHP version.');
        }
        try {
            $res = (array) $this->agent->call('php.install', ['version' => $version]);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/php', $e);
        }
        $this->jobs->track((string) $res['job_id'], 'Install PHP ' . $version, 'php', $version, $this->userId($request));
        $this->audit->record('php.install', 'php', $version, AuditLogger::SUCCESS, ['packages' => $res['packages'] ?? []]);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess('/php', 'Installing PHP ' . $version . '. You can follow the progress below.');
    }

    public function toggle(Request $request, string $version): Response
    {
        if (!preg_match('/^[5-9]\.[0-9]{1,2}$/', $version)) {
            return $this->redirectWithError('/php', 'Unknown PHP version.');
        }
        $enable = $request->input('enabled') === '1';
        try {
            $this->agent->call('php.set_enabled', ['version' => $version, 'enabled' => $enable]);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/php', $e);
        }
        $this->audit->record($enable ? 'php.enable' : 'php.disable', 'php', $version);
        return $this->redirectWithSuccess('/php', 'PHP ' . $version . ($enable ? ' started and enabled.' : ' stopped and disabled.'));
    }

    private static function validVersion(string $version): bool
    {
        return (bool) preg_match('/^[5-9]\.[0-9]{1,2}$/', $version);
    }

    /** Extensions, php.ini and PHP-FPM settings of one PHP version. */
    public function show(Request $request, string $version): Response
    {
        if (!self::validVersion($version)) {
            return $this->redirectWithError('/php', 'Unknown PHP version.');
        }
        $tab = in_array($request->query('tab'), ['extensions', 'ini', 'fpm'], true) ? (string) $request->query('tab') : 'extensions';
        $data = ['title' => 'PHP ' . $version, 'version' => $version, 'tab' => $tab, 'extensions' => [], 'ini' => null, 'fpm' => null, 'agentError' => null];
        try {
            $data['extensions'] = (array) $this->agent->call('php.extensions', ['version' => $version]);
            $data['ini'] = (array) $this->agent->call('php.ini_get', ['version' => $version]);
            $data['fpm'] = (array) $this->agent->call('php.fpm_get', ['version' => $version]);
        } catch (AgentException $e) {
            $data['agentError'] = $e;
        }
        return $this->render('php/show', $data);
    }

    public function extension(Request $request, string $version, string $op): Response
    {
        $to = '/php/' . $version . '?tab=extensions';
        $name = $request->string('name');
        if (!self::validVersion($version) || !preg_match('/^[a-z0-9_]{2,20}$/', $name) || !in_array($op, ['install', 'remove', 'toggle'], true)) {
            return $this->redirectWithError('/php', 'Unknown PHP version or extension.');
        }
        try {
            if ($op === 'toggle') {
                $on = $request->input('enabled') === '1';
                $this->agent->call('php.extension_set', ['version' => $version, 'name' => $name, 'enabled' => $on]);
                $this->audit->record($on ? 'php.extension_enable' : 'php.extension_disable', 'php', $version, AuditLogger::SUCCESS, ['extension' => $name]);
                return $this->redirectWithSuccess($to, $name . ($on ? ' switched on' : ' switched off') . ' for PHP ' . $version . '; PHP-FPM was reloaded.');
            }
            $action = $op === 'remove' ? 'php.extension_remove' : 'php.extension_install';
            $res = (array) $this->agent->call($action, ['version' => $version, 'name' => $name]);
        } catch (AgentException $e) {
            $this->audit->record('php.extension_' . $op, 'php', $version, AuditLogger::FAILURE, ['extension' => $name, 'error' => $e->getMessage()]);
            return $this->redirectWithFailure($to, $e);
        }
        $label = ($op === 'remove' ? 'Remove ' : 'Install ') . $name . ' (PHP ' . $version . ')';
        $this->jobs->track((string) $res['job_id'], $label, 'php', $version, $this->userId($request));
        $this->audit->record('php.extension_' . $op, 'php', $version, AuditLogger::SUCCESS, ['extension' => $name, 'package' => $res['package'] ?? '']);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess($to, $label . ' started.');
    }

    public function ini(Request $request, string $version): Response
    {
        $to = '/php/' . $version . '?tab=ini';
        if (!self::validVersion($version)) {
            return $this->redirectWithError('/php', 'Unknown PHP version.');
        }
        if ($request->input('mode') === 'text') {
            $content = (string) $request->input('content', '');
        } else {
            // The form: one field per setting; empty keeps PHP's own value.
            $lines = [];
            foreach ((array) $request->input('ini', []) as $key => $value) {
                $value = trim((string) $value);
                if ($value !== '' && preg_match('/^[a-z0-9_.]{2,64}$/', (string) $key)) {
                    $lines[] = $key . ' = ' . $value;
                }
            }
            $content = implode("\n", $lines);
        }
        try {
            $this->agent->call('php.ini_set', ['version' => $version, 'content' => $content]);
        } catch (AgentException $e) {
            $this->audit->record('php.ini', 'php', $version, AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure($to, $e);
        }
        $this->audit->record('php.ini', 'php', $version, AuditLogger::SUCCESS);
        return $this->redirectWithSuccess($to, 'php.ini settings of PHP ' . $version . ' saved; PHP-FPM was reloaded.');
    }

    public function fpm(Request $request, string $version): Response
    {
        $to = '/php/' . $version . '?tab=fpm';
        if (!self::validVersion($version)) {
            return $this->redirectWithError('/php', 'Unknown PHP version.');
        }
        $settings = ['version' => $version, 'log_level' => $request->string('log_level')];
        foreach (['emergency_restart_threshold', 'emergency_restart_interval_seconds', 'process_control_timeout_seconds', 'process_max'] as $k) {
            $raw = trim((string) $request->input($k, ''));
            $settings[$k] = $raw === '' ? 0 : (int) $raw;
        }
        try {
            $this->agent->call('php.fpm_set', $settings);
        } catch (AgentException $e) {
            $this->audit->record('php.fpm', 'php', $version, AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure($to, $e);
        }
        $this->audit->record('php.fpm', 'php', $version, AuditLogger::SUCCESS);
        return $this->redirectWithSuccess($to, 'PHP-FPM settings of PHP ' . $version . ' saved; PHP-FPM was reloaded.');
    }

    public function modules(Request $request, string $version): Response
    {
        if (!preg_match('/^[5-9]\.[0-9]{1,2}$/', $version)) {
            return Response::json(['success' => false], 404);
        }
        try {
            return Response::json(['success' => true, 'modules' => $this->agent->call('php.modules', ['version' => $version])]);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 502);
        }
    }
}
