<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\DiscoverySnapshotRepository;
use WhaCP\Services\DiscoveryService;
use WhaCP\Services\SettingsService;
use WhaCP\View\View;

/**
 * Read-only inventory pages: PHP, databases, certificates, cron, and the
 * discovery overview itself.
 */
final class DiscoveryController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly DiscoveryService $discovery,
        private readonly DiscoverySnapshotRepository $snapshots,
        private readonly SettingsService $settings,
        private readonly \WhaCP\Agent\AgentClient $agent,
        private readonly \WhaCP\Services\ServerContext $server,
        private readonly \WhaCP\Repositories\WebsiteRepository $websites,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->page($request, 'discovery/index', 'Discovery', fn (array $inv): array => [
            'inventory' => $inv,
            'history' => $this->snapshots->history(10, $this->server->id()),
        ]);
    }

    public function php(Request $request): Response
    {
        $available = [];
        $services = [];
        if ($this->view->can(\WhaCP\Security\Permissions::PHP_MANAGE)) {
            try {
                $available = (array) $this->agent->call('php.available');
                foreach ((array) $this->agent->call('services.status') as $svc) {
                    $services[(string) $svc['unit']] = $svc;
                }
            } catch (AgentException) {
            }
        }
        $this->view->share('php_available', $available);
        $this->view->share('php_services', $services);
        return $this->page($request, 'discovery/php', 'PHP', static function (array $inv): array {
            $usage = [];
            foreach (DiscoveryService::websites($inv) as $site) {
                if (isset($site['php']['version']) && $site['php']['version'] !== '') {
                    $usage[(string) $site['php']['version']][] = (string) $site['domain'];
                }
            }
            return ['php' => (array) ($inv['php'] ?? []), 'usage' => $usage];
        });
    }

    public function databases(Request $request): Response
    {
        return $this->page($request, 'discovery/databases', 'Databases', static fn (array $inv): array => [
            'db' => (array) ($inv['databases'] ?? []),
            'redis' => (array) ($inv['redis'] ?? []),
        ]);
    }

    public function ssl(Request $request): Response
    {
        // Administrators manage SSL/TLS in its own section; others see their certificates here.
        if (\WhaCP\Hosting\Scope::isAll($this->user($request))) {
            return Response::redirect('/ssl/hosts');
        }
        $warn = $this->settings->int('ssl_warning_days');
        return $this->page($request, 'discovery/ssl', 'SSL certificates', static fn (array $inv): array => [
            'certificates' => (array) ($inv['certificates'] ?? []),
            'sites' => DiscoveryService::websites($inv),
            'warnDays' => $warn,
        ]);
    }

    public function cron(Request $request): Response
    {
        return $this->page($request, 'discovery/cron', 'Cron jobs', static fn (array $inv): array => [
            'jobs' => (array) ($inv['cron']['jobs'] ?? []),
            'warnings' => (array) ($inv['cron']['warnings'] ?? []),
        ]);
    }

    public function run(Request $request): Response
    {
        try {
            $result = $this->discovery->run($this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithError('/discovery', $e->getMessage() . ' ' . $e->remedy());
        }
        $count = count(DiscoveryService::websites($result['data']));
        $back = (string) $request->input('return_to', '/discovery');
        if (!preg_match('#^/[a-z0-9/_-]*$#', $back)) {
            $back = '/discovery';
        }
        return $this->redirectWithSuccess($back, sprintf('Discovery completed: %d website(s) found. Nothing on the server was changed.', $count));
    }

    /**
     * @param callable(array<string, mixed>): array<string, mixed> $build
     */
    private function page(Request $request, string $template, string $title, callable $build): Response
    {
        try {
            $inventory = $this->discovery->inventory(false, $this->userId($request));
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => $title, 'error' => $e]);
        }
        return $this->render($template, $build($this->scoped($request, $inventory['data'])) + [
            'discoveredAt' => $inventory['created_at'],
        ]);
    }

    /**
     * @param array<string, mixed> $inventory
     * @return array<string, mixed>
     */
    private function scoped(Request $request, array $inventory): array
    {
        $user = $this->user($request);
        if (\WhaCP\Hosting\Scope::isAll($user)) {
            return $inventory;
        }
        $domains = array_map(static fn (array $w): string => (string) $w['domain'], $this->websites->all($this->server->id(), $user));
        return DiscoveryService::forScope($inventory, false, $domains);
    }
}
