<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Services\SettingsService;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/**
 * PHP Manager (in the manner of cPanel's EasyApache): choose the PHP
 * versions and extensions the server should have, review what that
 * changes (install, update, switch on or off, remove, unchanged, kept),
 * apply it as one job, and keep it as the server's PHP profile. The agent
 * computes the plan again when applying; the per-version PHP pages stay
 * as they were.
 */
final class PhpManagerController extends Controller
{
    /** Last day of security fixes per PHP release (php.net). */
    public const EOL = [
        '7.4' => '2022-11-28', '8.0' => '2023-11-26', '8.1' => '2025-12-31', '8.2' => '2026-12-31',
        '8.3' => '2027-12-31', '8.4' => '2028-12-31', '8.5' => '2029-12-31',
    ];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
        private readonly SettingsService $settings,
        private readonly Connection $db,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array{versions: list<string>, extensions: list<string>, job_id?: string, applied_at?: string} */
    private function saved(): array
    {
        $p = json_decode($this->settings->string('php_profile'), true);
        return is_array($p) && isset($p['versions'], $p['extensions'])
            ? ['versions' => array_values(array_map('strval', (array) $p['versions'])), 'extensions' => array_values(array_map('strval', (array) $p['extensions']))] + array_intersect_key($p, ['job_id' => 1, 'applied_at' => 1])
            : ['versions' => [], 'extensions' => []];
    }

    /**
     * What the server has now: versions with PHP-FPM, extensions installed
     * and on (per version), and the websites using each version.
     *
     * @return array{installed: list<string>, extensions: array<string, list<string>>, usage: array<string, int>}
     */
    private function current(): array
    {
        $installed = [];
        foreach ((array) (((array) $this->agent->call('discovery.php'))['versions'] ?? []) as $v) {
            if (!empty($v['fpm_binary'])) {
                $installed[] = (string) $v['version'];
            }
        }
        sort($installed);
        $ext = [];
        foreach ($installed as $v) {
            $ext[$v] = [];
            foreach ((array) $this->agent->call('php.extensions', ['version' => $v]) as $e) {
                if (!empty($e['installed']) && !empty($e['enabled'])) {
                    $ext[$v][] = (string) $e['name'];
                }
            }
        }
        $usage = [];
        foreach ($this->db->fetchAll('SELECT php_version, COUNT(*) AS n FROM websites WHERE server_id = ? AND php_version IS NOT NULL GROUP BY php_version', [$this->server->id()]) as $r) {
            $usage[(string) $r['php_version']] = (int) $r['n'];
        }
        return ['installed' => $installed, 'extensions' => $ext, 'usage' => $usage];
    }

    public function index(Request $request): Response
    {
        try {
            $current = $this->current();
            $available = array_map('strval', (array) $this->agent->call('php.available'));
            $catalog = (array) $this->agent->call('php.catalog');
        } catch (AgentException $e) {
            return $this->render('php/manager', ['agentError' => $e]);
        }
        $saved = $this->saved();
        // Until a profile is saved, the selection is what the server has.
        $selection = $saved['versions'] !== [] ? $saved : [
            'versions' => $current['installed'],
            'extensions' => $current['installed'] === [] ? array_map('strval', (array) ($catalog['defaults'] ?? [])) : array_values(array_unique(array_merge(...array_values($current['extensions'] ?: [[]])))),
        ];
        $versions = array_values(array_unique(array_merge($available, $current['installed'], $selection['versions'])));
        usort($versions, 'version_compare');
        return $this->render('php/manager', [
            'versions' => $versions, 'available' => $available, 'current' => $current, 'catalog' => (array) ($catalog['extensions'] ?? []),
            'selection' => $selection, 'saved' => $saved, 'eol' => self::EOL,
        ]);
    }

    /**
     * The profile from the form.
     *
     * @return array{versions: list<string>, extensions: list<string>}
     */
    private function fromRequest(Request $request): array
    {
        $versions = array_values(array_filter(array_map('strval', (array) $request->input('versions', [])), static fn (string $v): bool => (bool) preg_match('/^[5-9]\.[0-9]{1,2}$/', $v)));
        $exts = array_values(array_filter(array_map('strval', (array) $request->input('extensions', [])), static fn (string $e): bool => (bool) preg_match('/^[a-z][a-z0-9_]{1,30}$/', $e)));
        return ['versions' => $versions, 'extensions' => $exts];
    }

    /** The review: what applying the selection would change. */
    public function review(Request $request): Response
    {
        $profile = $this->fromRequest($request);
        if ($profile['versions'] === []) {
            return $this->redirectWithError('/php/manager', 'Choose at least one PHP version.');
        }
        return $this->showReview($profile);
    }

    /** @param array{versions: list<string>, extensions: list<string>} $profile */
    private function showReview(array $profile): Response
    {
        try {
            $plan = (array) $this->agent->call('php.profile_plan', $profile, 120);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/php/manager', $e);
        }
        return $this->render('php/manager_review', ['profile' => $profile, 'plan' => $plan, 'eol' => self::EOL]);
    }

    public function apply(Request $request): Response
    {
        $profile = $this->fromRequest($request);
        if ($profile['versions'] === []) {
            return $this->redirectWithError('/php/manager', 'Choose at least one PHP version.');
        }
        try {
            $res = (array) $this->agent->call('php.profile_apply', $profile + ['confirm_removals' => $request->input('confirm_removals') === '1'], 120);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/php/manager', $e);
        }
        $job = (string) $res['job_id'];
        $this->jobs->track($job, 'Apply PHP profile', 'server', (string) $this->server->id(), $this->userId($request));
        $this->audit->record('php.profile_apply', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $profile);
        try {
            $this->settings->update(['php_profile' => (string) json_encode($profile + ['job_id' => $job, 'applied_at' => gmdate('Y-m-d H:i')])], $this->userId($request));
        } catch (ValidationException) {
            // The profile is applied either way; it is just not remembered.
        }
        $this->flashJob($job);
        return $this->redirectWithSuccess('/php/manager', 'Applying the PHP profile… Follow the progress in the job log.');
    }

    /** Downloads the saved profile (to use on another server). */
    public function export(Request $request): Response
    {
        $p = $this->saved();
        $body = (string) json_encode(['format' => 'whacp-php-profile', 'version' => 1, 'versions' => $p['versions'], 'extensions' => $p['extensions']], JSON_PRETTY_PRINT);
        return new Response($body . "\n", 200, ['Content-Type' => 'application/json', 'Content-Disposition' => 'attachment; filename="whacp-php-profile.json"', 'Cache-Control' => 'no-store']);
    }

    /** Reviews a profile file from another server (nothing is applied yet). */
    public function import(Request $request): Response
    {
        $up = $_FILES['profile'] ?? null;
        if (!is_array($up) || ($up['error'] ?? UPLOAD_ERR_NO_FILE) !== UPLOAD_ERR_OK || !is_uploaded_file((string) $up['tmp_name']) || (int) $up['size'] > 65536) {
            return $this->redirectWithError('/php/manager', 'Choose a PHP profile file (whacp-php-profile.json).');
        }
        $p = json_decode((string) file_get_contents((string) $up['tmp_name']), true);
        if (!is_array($p) || ($p['format'] ?? '') !== 'whacp-php-profile' || !is_array($p['versions'] ?? null) || !is_array($p['extensions'] ?? null)) {
            return $this->redirectWithError('/php/manager', 'This is not a NimVor PHP profile file.');
        }
        $profile = ['versions' => array_values(array_filter(array_map('strval', $p['versions']), static fn (string $v): bool => (bool) preg_match('/^[5-9]\.[0-9]{1,2}$/', $v))),
            'extensions' => array_values(array_filter(array_map('strval', $p['extensions']), static fn (string $e): bool => (bool) preg_match('/^[a-z][a-z0-9_]{1,30}$/', $e)))];
        if ($profile['versions'] === []) {
            return $this->redirectWithError('/php/manager', 'The profile has no PHP version.');
        }
        return $this->showReview($profile);
    }
}
