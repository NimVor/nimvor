<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\DnsService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class DnsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly DnsService $dns,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        $status = null;
        $error = null;
        try {
            $status = (array) $this->agent->call('dns.status');
        } catch (AgentException $e) {
            $error = $e;
        }
        $served = [];
        foreach ((array) ($status['zones'] ?? []) as $z) {
            $served[(string) ((array) $z)['zone']] = (int) ((array) $z)['serial'];
        }
        return $this->render('dns/index', [
            'status' => $status, 'agentError' => $error, 'zones' => $this->dns->zones($user), 'served' => $served,
            'websites' => $this->sites->all($this->server->id(), $user), 'nameservers' => $this->dns->nameservers(),
            'admin' => Scope::isAll($user), 'preselect' => (int) $request->query('website', 0),
        ]);
    }

    /** @param callable(): string $fn */
    private function act(Request $request, string $to, callable $fn): Response
    {
        try {
            return $this->redirectWithSuccess($to, $fn());
        } catch (ValidationException $e) {
            return $this->backWithErrors($to, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }

    public function install(Request $request): Response
    {
        try {
            $res = (array) $this->agent->call('packages.install', ['name' => 'bind9']);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/dns', $e);
        }
        $this->jobs->track((string) $res['job_id'], 'Install BIND', 'server', 'dns', $this->userId($request));
        $this->audit->record('packages.install', 'server', 'dns', AuditLogger::SUCCESS, ['name' => 'bind9']);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess('/dns', 'Installing BIND. Then set it up for WhaCP below.');
    }

    public function setup(Request $request): Response
    {
        return $this->act($request, '/dns', function () use ($request): string {
            $res = (array) $this->agent->call('dns.configure', ['confirm_edit_package_config' => $request->input('confirm') === '1'], 90);
            $this->audit->record('dns.configure', 'server', 'dns', AuditLogger::SUCCESS, ['changed' => $res['changed'] ?? []]);
            return 'The DNS server is ready. ' . implode(' ', array_map('strval', (array) ($res['notes'] ?? [])));
        });
    }

    public function store(Request $request): Response
    {
        $id = 0;
        $res = $this->act($request, '/dns', function () use ($request, &$id): string {
            $id = $this->dns->create($request->all(), $this->user($request));
            return 'Zone created and loaded. Point the domain\'s name servers at your registrar to ' . implode(', ', $this->dns->nameservers()) . '.';
        });
        return $id > 0 ? Response::redirect('/dns/zones/' . $id) : $res;
    }

    public function show(Request $request, string $id): Response
    {
        $zone = $this->dns->zone((int) $id, $this->user($request));
        if ($zone === null) {
            return $this->render('errors/error', ['title' => 'Zone not found', 'message' => 'This zone does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $served = null;
        try {
            foreach ((array) (((array) $this->agent->call('dns.status'))['zones'] ?? []) as $z) {
                if (((array) $z)['zone'] === $zone['name']) {
                    $served = (int) ((array) $z)['serial'];
                }
            }
        } catch (AgentException) {
        }
        return $this->render('dns/zone', [
            'zone' => $zone, 'records' => $this->dns->records((int) $zone['id']), 'served' => $served,
            'edit' => (int) $request->query('edit', 0),
        ]);
    }

    /** @param callable(array<string, mixed>): string $fn */
    private function zoneAct(Request $request, string $id, callable $fn): Response
    {
        $zone = $this->dns->zone((int) $id, $this->user($request));
        if ($zone === null) {
            return $this->redirectWithError('/dns', 'Zone not found.');
        }
        return $this->act($request, '/dns/zones/' . (int) $zone['id'], static fn (): string => $fn($zone));
    }

    public function addRecord(Request $request, string $id): Response
    {
        return $this->zoneAct($request, $id, function (array $zone) use ($request): string {
            $this->dns->saveRecord($zone, null, $request->all());
            return 'Record added; the zone was reloaded.';
        });
    }

    public function updateRecord(Request $request, string $id, string $record): Response
    {
        return $this->zoneAct($request, $id, function (array $zone) use ($request, $record): string {
            $this->dns->saveRecord($zone, (int) $record, $request->all());
            return 'Record saved; the zone was reloaded.';
        });
    }

    public function deleteRecord(Request $request, string $id, string $record): Response
    {
        return $this->zoneAct($request, $id, function (array $zone) use ($record): string {
            $this->dns->saveRecord($zone, (int) $record, null, true);
            return 'Record deleted; the zone was reloaded.';
        });
    }

    public function settings(Request $request, string $id): Response
    {
        return $this->zoneAct($request, $id, function (array $zone) use ($request): string {
            $this->dns->settings($zone, (int) $request->input('ttl', 3600), $request->string('secondaries'));
            return 'Zone settings saved.';
        });
    }

    public function sync(Request $request, string $id): Response
    {
        return $this->zoneAct($request, $id, function (array $zone): string {
            $this->dns->sync($zone);
            return 'The zone was loaded again.';
        });
    }

    public function delete(Request $request, string $id): Response
    {
        $zone = $this->dns->zone((int) $id, $this->user($request));
        if ($zone === null) {
            return $this->redirectWithError('/dns', 'Zone not found.');
        }
        try {
            $this->dns->delete($zone, $request->string('confirm'));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/dns/zones/' . (int) $zone['id'], $e, []);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/dns/zones/' . (int) $zone['id'], $e);
        }
        return $this->redirectWithSuccess('/dns', 'Zone ' . $zone['name'] . ' deleted.');
    }

    /** Website tab: the website's zone, or the form to create it. */
    public function site(Request $request, string $id): Response
    {
        $site = $this->sites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        $zone = $this->dns->forWebsite((int) $site['id']);
        if ($zone !== null && $this->dns->zone((int) $zone['id'], $this->user($request)) !== null) {
            return Response::redirect('/dns/zones/' . (int) $zone['id']);
        }
        return Response::redirect('/dns?website=' . (int) $site['id']);
    }
}
