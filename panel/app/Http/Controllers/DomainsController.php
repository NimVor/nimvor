<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\DiscoveryService;
use WhaCP\View\View;

/**
 * Every hostname served by the server (primary names and aliases), with
 * the website it belongs to and whether its certificate covers it.
 */
final class DomainsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly DiscoveryService $discovery,
        private readonly \WhaCP\Services\ServerContext $server,
        private readonly \WhaCP\Repositories\WebsiteRepository $websites,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        try {
            $inventory = $this->discovery->inventory(false, $this->userId($request));
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => 'Domains', 'error' => $e]);
        }
        $rows = [];
        foreach (DiscoveryService::websites($this->scoped($request, $inventory['data'])) as $site) {
            $names = array_merge([(string) $site['domain']], array_map('strval', (array) ($site['aliases'] ?? [])));
            foreach ($names as $i => $name) {
                $rows[] = [
                    'name' => $name,
                    'primary' => $i === 0,
                    'site' => $site,
                    'covered' => self::covers((array) ($site['ssl']['names'] ?? []), $name, $i === 0 && !empty($site['ssl']['covers_domain'])),
                ];
            }
        }
        usort($rows, static fn (array $a, array $b): int => [self::apex($a['name']), $a['name']] <=> [self::apex($b['name']), $b['name']]);
        return $this->render('domains/index', ['rows' => $rows, 'discoveredAt' => $inventory['created_at']]);
    }

    /** @param list<string> $names */
    private static function covers(array $names, string $host, bool $fallback): bool
    {
        foreach ($names as $n) {
            if ($n === $host || (str_starts_with($n, '*.') && str_ends_with($host, substr($n, 1)) && substr_count($host, '.') === substr_count($n, '.'))) {
                return true;
            }
        }
        return $fallback;
    }

    private static function apex(string $host): string
    {
        $parts = explode('.', $host);
        return implode('.', array_slice($parts, -2));
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
