<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\IpService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * IP addresses (server administrators): the pool, IPv6 ranges, the
 * migration wizard and binding a website to addresses.
 */
final class IpController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly IpService $ips,
        private readonly WebsiteRepository $websites,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'IP addresses are managed by server administrators.', 'status' => 403], 403);
    }

    /** @param callable(): string $fn */
    private function act(Request $request, string $to, callable $fn): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            return $this->redirectWithSuccess($to, $fn());
        } catch (ValidationException $e) {
            return $this->backWithErrors($to, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }

    public function index(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $error = null;
        $server = null;
        try {
            $server = $this->ips->serverAddresses();
        } catch (AgentException $e) {
            $error = $e;
        }
        $this->ips->ensurePool();
        $pool = $this->ips->pool();
        $inPool = array_flip(array_column($pool, 'address'));
        $unpooled = [];
        foreach ((array) ($server['addresses'] ?? []) as $a) {
            $ip = IpService::canonical((string) $a['address']);
            if ($ip !== null && IpService::usable($ip) && ($a['scope'] ?? '') !== 'link' && !isset($inPool[$ip])) {
                $unpooled[] = ['address' => $ip, 'interface' => (string) $a['interface'], 'prefix_len' => (int) $a['prefix_len']];
            }
        }
        return $this->render('ips/index', ['tab' => 'addresses', 'pool' => $pool, 'unpooled' => $unpooled, 'server' => $server, 'agentError' => $error, 'rebuild' => $this->session->pullFlash('ip_rebuild')]);
    }

    public function store(Request $request): Response
    {
        return $this->act($request, '/ips', function () use ($request): string {
            $this->ips->add($request->string('address'), $request->string('kind', 'shared'), $request->string('reason'));
            return 'Address added to the pool.';
        });
    }

    public function update(Request $request, string $id): Response
    {
        return $this->act($request, '/ips', function () use ($request, $id): string {
            $this->ips->update((int) $id, $request->string('kind', 'shared'), $request->input('reserved') === '1', $request->string('reason'));
            return 'Address saved.';
        });
    }

    public function delete(Request $request, string $id): Response
    {
        return $this->act($request, '/ips', function () use ($id): string {
            $this->ips->delete((int) $id);
            return 'Address removed from the pool (the server keeps it on its interface).';
        });
    }

    public function rebuild(Request $request): Response
    {
        return $this->act($request, '/ips', function (): string {
            $res = $this->ips->rebuild();
            $this->session->flash('ip_rebuild', $res);
            return 'Pool rebuilt: ' . count($res['added']) . ' address(es) added' . ($res['missing'] !== [] ? ', ' . count($res['missing']) . ' no longer on the server (kept, listed below)' : '') . '.';
        });
    }

    public function ranges(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $pool = array_values(array_filter($this->ips->pool(), static fn (array $a): bool => $a['range_id'] !== null));
        $sites = $this->websites->all($this->server->id(), $this->user($request));
        return $this->render('ips/ranges', ['tab' => 'ranges', 'ranges' => $this->ips->ranges(), 'allocated' => $pool, 'websites' => $sites, 'agentError' => null]);
    }

    public function storeRange(Request $request): Response
    {
        return $this->act($request, '/ips/ranges', function () use ($request): string {
            $this->ips->addRange($request->string('prefix'), $request->string('note'));
            return 'Range added; its addresses are now local to this server.';
        });
    }

    public function updateRange(Request $request, string $id): Response
    {
        return $this->act($request, '/ips/ranges', function () use ($request, $id): string {
            $this->ips->updateRange((int) $id, $request->string('note'));
            return 'Range saved.';
        });
    }

    public function deleteRange(Request $request, string $id): Response
    {
        return $this->act($request, '/ips/ranges', function () use ($id): string {
            $this->ips->removeRange((int) $id);
            return 'Range removed.';
        });
    }

    public function allocate(Request $request, string $id): Response
    {
        return $this->act($request, '/ips/ranges', function () use ($request, $id): string {
            $ip = $this->ips->allocate((int) $id, $request->string('address'), $request->string('kind', 'dedicated'));
            $siteId = (int) $request->input('website_id', 0);
            if ($siteId > 0) {
                $site = $this->websites->find($this->server->id(), $siteId, $this->user($request)) ?? throw new UserFacingException('That website does not exist.');
                $res = $this->ips->assign($site, (string) ($site['ipv4'] ?? ''), $ip, $request->input('dns') === '1');
                return $ip . ' allocated and assigned to ' . $site['domain'] . ($res['dns_records'] > 0 ? '; ' . $res['dns_records'] . ' DNS record(s) updated' : '') . '.';
            }
            return $ip . ' allocated.';
        });
    }

    public function migrate(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $plan = null;
        $source = (string) $request->query('source', '');
        $dest = (string) $request->query('dest', '');
        $errors = [];
        if ($source !== '' || $dest !== '') {
            try {
                $plan = $this->ips->migrationPlan($source, $dest, $request->query('all_zones') === '1');
            } catch (ValidationException $e) {
                $errors = $e->errors();
            }
        }
        $pool = array_values(array_filter($this->ips->pool(), static fn (array $a): bool => (int) $a['reserved'] === 0 || $a['sites'] !== []));
        return $this->render('ips/migrate', ['tab' => 'migrate', 'pool' => $pool, 'plan' => $plan, 'errors' => $errors, 'q' => ['source' => $source, 'dest' => $dest, 'all_zones' => $request->query('all_zones') === '1'], 'results' => $this->session->pullFlash('ip_migration'), 'agentError' => null]);
    }

    public function runMigration(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        if ($request->input('confirm') !== '1') {
            return $this->redirectWithError('/ips/migrate', 'Confirm the migration first.');
        }
        try {
            $results = $this->ips->migrate($request->string('source'), $request->string('dest'), $request->input('all_zones') === '1');
        } catch (ValidationException $e) {
            return $this->backWithErrors('/ips/migrate', $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/ips/migrate', $e);
        }
        $this->session->flash('ip_migration', $results);
        $failed = count(array_filter($results, static fn (array $r): bool => !$r['ok']));
        return $failed > 0 ? $this->redirectWithError('/ips/migrate', $failed . ' step(s) failed; see the results.') : $this->redirectWithSuccess('/ips/migrate', 'Migration complete.');
    }

    /** A website's addresses (from its Advanced page). */
    public function assignSite(Request $request, string $id): Response
    {
        $to = '/sites/' . (int) $id . '/advanced#ip';
        return $this->act($request, $to, function () use ($request, $id): string {
            $site = $this->websites->find($this->server->id(), (int) $id, $this->user($request)) ?? throw new UserFacingException('That website does not exist.');
            $res = $this->ips->assign($site, $request->string('ipv4'), $request->string('ipv6'), $request->input('dns') === '1');
            return 'Addresses saved and the website reloaded' . ($res['dns_records'] > 0 ? '; ' . $res['dns_records'] . ' DNS record(s) updated' : '') . '.';
        });
    }
}
