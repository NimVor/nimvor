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
        private readonly \WhaCP\Repositories\WebsiteRepository $managed,
        private readonly \WhaCP\Services\ServerContext $server,
        private readonly \WhaCP\Database\Connection $db,
        private readonly \WhaCP\Services\PlanLimits $limits,
        private readonly \WhaCP\Services\FtpUsage $ftp,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        $managed = $this->managed->all($this->server->id(), $user);
        $q = mb_strtolower(trim((string) $request->query('q', '')));
        if ($q !== '') {
            $managed = array_values(array_filter($managed, static fn (array $s): bool => str_contains(mb_strtolower($s['domain'] . ' ' . implode(' ', $s['aliases'])), $q)));
        }
        // Cards (the customers' default) or the table.
        $layout = $request->query('view', '') === 'list' || ($request->query('view', '') !== 'grid' && !$this->view->simpleLayout()) ? 'list' : 'grid';
        $list = ['backups' => $this->backupCounts(array_column($managed, 'id')), 'ssl' => [], 'nginxVersion' => null, 'planUsage' => null];
        // Hosting customers see their plan's limits and what they use.
        if ($this->view->simpleLayout() && ($plan = $this->limits->planFor((int) $user['id'])) !== null) {
            $external = [];
            try {
                $external['ftp_accounts'] = $this->ftp->count($this->limits->owners((int) $user['id']));
            } catch (AgentException) {
                // shown without FTP accounts
            }
            $list['planUsage'] = ['name' => (string) $plan['name'], 'rows' => $this->limits->usage((int) $user['id'], $external)];
        }
        // Hosting customers only see their own (managed) websites.
        if (!\WhaCP\Hosting\Scope::isAll($user)) {
            if (!$this->view->simpleLayout()) {
                // Resellers: certificate details of their own websites only.
                try {
                    $scoped = DiscoveryService::forScope($this->discovery->inventory(false, $this->userId($request))['data'], false, array_column($managed, 'domain'));
                    $list['ssl'] = self::sslByDomain($scoped);
                } catch (AgentException) {
                    // The table shows HTTPS on/off without the expiry.
                }
            }
            return $this->render('websites/index', ['managed' => $managed, 'sites' => [], 'total' => 0, 'q' => $q, 'status' => '', 'discoveredAt' => null, 'catchAll' => 0, 'warnings' => [], 'warnDays' => $this->settings->int('ssl_warning_days'), 'layout' => $layout] + $list);
        }
        try {
            $inventory = $this->discovery->inventory(false, $this->userId($request));
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => 'Websites', 'error' => $e]);
        }
        $list['ssl'] = self::sslByDomain($inventory['data']);
        $list['nginxVersion'] = isset($inventory['data']['websites']['nginx_version']) ? (string) $inventory['data']['websites']['nginx_version'] : null;
        // Sites created by NimVor are listed as managed, not as discovered.
        $sites = array_values(array_filter(
            DiscoveryService::websites($inventory['data']),
            static function (array $s): bool {
                foreach ((array) ($s['config_files'] ?? []) as $f) {
                    if (str_starts_with(basename((string) $f), 'whacp-')) {
                        return false;
                    }
                }
                return true;
            },
        ));

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
            'managed' => $managed,
            'sites' => $filtered,
            'total' => count($sites),
            'q' => $q,
            'status' => $status,
            'discoveredAt' => $inventory['created_at'],
            'catchAll' => (int) ($inventory['data']['websites']['catch_all_vhosts'] ?? 0),
            'warnings' => (array) ($inventory['data']['websites']['warnings'] ?? []),
            'warnDays' => $this->settings->int('ssl_warning_days'),
            'layout' => $layout,
        ] + $list);
    }

    /**
     * Completed backups per website.
     *
     * @param list<int|string> $ids
     * @return array<int, int>
     */
    private function backupCounts(array $ids): array
    {
        if ($ids === []) {
            return [];
        }
        $in = implode(',', array_fill(0, count($ids), '?'));
        $out = [];
        foreach ($this->db->fetchAll("SELECT website_id, COUNT(*) AS n FROM backups WHERE status = 'completed' AND website_id IN ({$in}) GROUP BY website_id", array_map('intval', $ids)) as $r) {
            $out[(int) $r['website_id']] = (int) $r['n'];
        }
        return $out;
    }

    /**
     * Certificate state per domain from the discovery snapshot.
     *
     * @param array<string, mixed> $inventory
     * @return array<string, array{days: int, status: string}>
     */
    private static function sslByDomain(array $inventory): array
    {
        $out = [];
        foreach (DiscoveryService::websites($inventory) as $w) {
            if (!empty($w['ssl']) && isset($w['domain'])) {
                $out[strtolower((string) $w['domain'])] = ['days' => (int) ($w['ssl']['days_remaining'] ?? 0), 'status' => (string) ($w['ssl']['status'] ?? '')];
            }
        }
        return $out;
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
