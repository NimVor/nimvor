<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\DiscoveryService;
use WhaCP\Services\SettingsService;
use WhaCP\View\View;

/**
 * Existing websites, discovered read-only from the server.
 */
final class WebsitesController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly DiscoveryService $discovery,
        private readonly SettingsService $settings,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        try {
            $inventory = $this->discovery->inventory(false, $this->userId($request));
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => 'Websites', 'error' => $e]);
        }
        $sites = DiscoveryService::websites($inventory['data']);

        $q = mb_strtolower(trim((string) $request->query('q', '')));
        $status = (string) $request->query('status', '');
        $filtered = array_values(array_filter($sites, static function (array $s) use ($q, $status): bool {
            if ($q !== '') {
                $hay = mb_strtolower(implode(' ', array_merge([(string) ($s['domain'] ?? '')], (array) ($s['aliases'] ?? []), [(string) ($s['document_root'] ?? '')])));
                if (!str_contains($hay, $q)) {
                    return false;
                }
            }
            return match ($status) {
                'enabled' => !empty($s['enabled']),
                'disabled' => empty($s['enabled']),
                'https' => !empty($s['https']),
                'nohttps' => empty($s['https']),
                default => true,
            };
        }));

        return $this->render('websites/index', [
            'sites' => $filtered,
            'total' => count($sites),
            'q' => $q,
            'status' => $status,
            'discoveredAt' => $inventory['created_at'],
            'catchAll' => (int) ($inventory['data']['websites']['catch_all_vhosts'] ?? 0),
            'warnings' => (array) ($inventory['data']['websites']['warnings'] ?? []),
            'warnDays' => $this->settings->int('ssl_warning_days'),
        ]);
    }

    public function show(Request $request, string $id): Response
    {
        try {
            $inventory = $this->discovery->inventory(false, $this->userId($request));
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => 'Website', 'error' => $e]);
        }
        $site = DiscoveryService::website($inventory['data'], $id);
        if ($site === null) {
            return $this->render('errors/error', [
                'title' => 'Website not found',
                'message' => 'This website was not found in the latest discovery snapshot. It may have been removed or renamed.',
                'status' => 404,
            ], 404);
        }
        $cron = array_values(array_filter((array) ($inventory['data']['cron']['jobs'] ?? []), static function (array $job) use ($site): bool {
            $root = (string) ($site['document_root'] ?? '');
            $base = $root === '' ? '' : dirname($root);
            return ($root !== '' && str_contains((string) $job['command'], $base)) || str_contains((string) $job['command'], (string) $site['domain']);
        }));
        return $this->render('websites/show', [
            'site' => $site,
            'cron' => $cron,
            'discoveredAt' => $inventory['created_at'],
            'warnDays' => $this->settings->int('ssl_warning_days'),
        ]);
    }
}
