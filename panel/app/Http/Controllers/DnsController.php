<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\AuditLogger;
use NimVor\Services\DnsService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

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
        return $this->redirectWithSuccess('/dns', 'Installing BIND. Then set it up for NimVor below.');
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
        // The website's own set-up page may ask to get errors back (only that page).
        $back = preg_match('#^/sites/\d+/dns$#', $request->string('return_to')) === 1 ? $request->string('return_to') : '/dns';
        $res = $this->act($request, $back, function () use ($request, &$id): string {
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
        $records = $this->dns->records((int) $zone['id']);
        $sort = in_array($request->query('sort'), ['name', 'type', 'ttl', 'value'], true) ? (string) $request->query('sort') : '';
        if ($sort !== '') {
            usort($records, static fn (array $a, array $b): int => ($sort === 'ttl' ? ((int) $a['ttl'] <=> (int) $b['ttl']) : strcmp((string) $a[$sort], (string) $b[$sort])) ?: strcmp((string) $a['name'], (string) $b['name']));
        }
        return $this->render('dns/zone', [
            'zone' => $zone, 'records' => $records, 'served' => $served, 'sort' => $sort,
            'warnings' => DnsService::warnings((string) $zone['name'], $records),
            'import' => $this->session->pullFlash('dns_import'),
            // The website this zone belongs to, when the user may see it: the page then keeps its menu.
            'site' => (int) ($zone['website_id'] ?? 0) > 0 ? $this->sites->find($this->server->id(), (int) $zone['website_id'], $this->user($request)) : null,
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

    public function bulk(Request $request, string $id): Response
    {
        return $this->zoneAct($request, $id, function (array $zone) use ($request): string {
            $op = $request->string('op');
            $n = $this->dns->bulk($zone, array_map('intval', (array) $request->input('ids', [])), $op);
            return $n . ' record(s) ' . ['enable' => 'turned on', 'disable' => 'turned off', 'delete' => 'deleted'][$op] . '; the zone was reloaded.';
        });
    }

    public function import(Request $request, string $id): Response
    {
        return $this->zoneAct($request, $id, function (array $zone) use ($request): string {
            $text = $request->string('zonefile');
            $up = $_FILES['zonefile_upload'] ?? null;
            if (is_array($up) && ($up['error'] ?? UPLOAD_ERR_NO_FILE) === UPLOAD_ERR_OK && is_uploaded_file((string) $up['tmp_name']) && (int) $up['size'] <= 1048576) {
                $text = (string) file_get_contents((string) $up['tmp_name']);
            }
            $res = $this->dns->import($zone, $text);
            $this->session->flash('dns_import', $res['skipped']);
            return $res['added'] . ' record(s) imported' . ($res['duplicates'] > 0 ? ', ' . $res['duplicates'] . ' already in the zone (name servers at @ are kept as they are)' : '') . (count($res['skipped']) > 0 ? '; ' . count($res['skipped']) . ' line(s) skipped (listed below)' : '') . '.';
        });
    }

    public function export(Request $request, string $id): Response
    {
        $zone = $this->dns->zone((int) $id, $this->user($request));
        if ($zone === null) {
            return $this->redirectWithError('/dns', 'Zone not found.');
        }
        try {
            $text = $this->dns->export($zone);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/dns/zones/' . (int) $zone['id'], $e);
        }
        return new Response($text, 200, ['Content-Type' => 'text/plain; charset=utf-8', 'Content-Disposition' => 'attachment; filename="' . $zone['name'] . '.zone"', 'X-Content-Type-Options' => 'nosniff']);
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
        $user = $this->user($request);
        $site = $this->sites->find($this->server->id(), (int) $id, $user);
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        $zone = $this->dns->forWebsite((int) $site['id']);
        if ($zone !== null && $this->dns->zone((int) $zone['id'], $user) !== null) {
            return Response::redirect('/dns/zones/' . (int) $zone['id']);
        }
        if ($zone !== null) {
            return $this->redirectWithError('/sites/' . (int) $site['id'], 'The DNS zone of this domain is managed by another account.');
        }
        $status = null;
        try {
            $status = (array) $this->agent->call('dns.status');
        } catch (AgentException) {
        }
        return $this->render('dns/site_setup', [
            'site' => $site, 'status' => $status, 'nameservers' => $this->dns->nameservers(),
            'ready' => !empty($status['configured']) && !empty($status['running']),
        ]);
    }
}
