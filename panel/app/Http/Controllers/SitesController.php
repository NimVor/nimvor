<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\UserRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Services\WebsiteService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * NimVor-managed websites (create, settings, enable/disable, delete).
 */
final class SitesController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly WebsiteRepository $sites,
        private readonly WebsiteService $service,
        private readonly ServerContext $server,
        private readonly AgentClient $agent,
        private readonly UserRepository $users,
        private readonly \NimVor\Services\ImportService $imports,
        private readonly JobTracker $jobs,
        private readonly \NimVor\Services\SiteOverview $overview,
        private readonly \NimVor\Services\DnsService $dns,
        private readonly \NimVor\Services\IpService $ips,
        private readonly \NimVor\Services\PlanService $plans,
        private readonly \NimVor\Services\AddonConversionService $conversion,
        private readonly \NimVor\Services\FeatureService $features,
        private readonly \NimVor\Services\SiteHealthService $health,
        private readonly \NimVor\Services\RuntimeService $runtimes,
        private readonly \NimVor\Services\WorkerService $workers,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    private function site(Request $request, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($request));
    }

    private function notFound(): Response
    {
        return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
    }

    public function create(Request $request): Response
    {
        $php = $this->service->phpVersions();
        $user = $this->user($request);
        // Which site-creation types this account's plan includes (staff: all).
        $allowedTypes = [];
        foreach (['type_php', 'type_static', 'type_nodejs', 'type_python', 'type_proxy'] as $t) {
            $allowedTypes[$t] = $this->features->enabledFor($user, $t);
        }
        return $this->render('sites/form', [
            'site' => null,
            'phpVersions' => $php,
            'defaultPhp' => $this->service->defaultPhpVersion($php),
            'hardenDefault' => $this->service->hardenNewSites(),
            'profileDefault' => $this->service->defaultPhpProfile(),
            'owners' => \NimVor\Hosting\Scope::isAll($user) ? $this->users->all() : [],
            'allowedTypes' => $allowedTypes,
            'runtimes' => $this->runtimes->selectable(),
        ]);
    }

    public function store(Request $request): Response
    {
        $input = $request->all();
        $appKind = in_array((string) ($input['kind'] ?? ''), ['nodejs', 'python'], true);
        try {
            $id = $this->service->create($input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/websites/new', $e, $input);
        } catch (UserFacingException $e) {
            $this->session->flash('old', array_diff_key($input, ['_token' => 1]));
            return $this->redirectWithFailure('/websites/new', $e);
        }
        // For a Node.js/Python site, start the app as a Worker on the chosen
        // runtime (best effort — the Runtime page lets them fix it otherwise).
        if ($appKind) {
            $this->provisionApp($request, $id, $input);
            return $this->redirectWithSuccess('/sites/' . $id . '/' . ($input['kind'] === 'python' ? 'python' : 'nodejs'), 'Website created. Set up your app below.');
        }
        return $this->redirectWithSuccess('/sites/' . $id, 'Website created. Nginx and PHP-FPM were validated and reloaded.');
    }

    /**
     * Create the "app" Worker for a new Node.js/Python site when a start
     * command was given. Never fails the site creation.
     *
     * @param array<string, mixed> $input
     */
    private function provisionApp(Request $request, int $siteId, array $input): void
    {
        $command = trim((string) ($input['app_command'] ?? ''));
        if ($command === '') {
            return;
        }
        $site = $this->sites->find($this->server->id(), $siteId, null);
        if ($site === null) {
            return;
        }
        $port = preg_match('/:(\d+)/', (string) $site['proxy_pass'], $m) ? $m[1] : '3000';
        try {
            $this->workers->save($site, null, [
                'name' => 'app',
                'command' => $command,
                'directory' => trim((string) ($input['app_dir'] ?? ''), " /"),
                'runtime' => (string) $site['app_runtime'],
                'env' => "PORT=" . $port . "\n" . (string) ($input['app_env'] ?? ''),
                'restart' => 'always',
                'procs' => 1,
                'memory_mb' => 0,
                'enabled' => true,
            ]);
        } catch (ValidationException | UserFacingException | AgentException) {
            // The site exists; the Runtime page lets them start the app.
        }
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        if (!$this->imports->refresh($site)) {
            return $this->redirectWithError('/websites', 'The import of ' . $site['domain'] . ' failed; the old website is unchanged. See the job log for details.');
        }
        $site = $this->site($request, $id) ?? $site;
        [$inspect, $inspectError] = $this->inspect($site);
        return $this->render('sites/show', [
            'site' => $site,
            'inspect' => $inspect,
            'inspectError' => $inspectError,
            'summary' => $this->overview->summary($site, $this->user($request)),
            'serverIp' => $this->overview->serverIp(),
            'dbBytes' => $this->overview->databaseBytes($site, $this->user($request)),
            'tab' => 'overview',
            'import' => $this->imports->importOf((int) $site['id']),
        ]);
    }

    /** Subdomains created from this website (their own page). */
    public function subdomains(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $children = $this->sites->children($this->server->id(), (int) $site['id']);
        $sub = array_values(array_filter($children, static fn (array $c): bool => str_ends_with((string) $c['domain'], '.' . $site['domain'])));
        $zone = $this->dns->forWebsite((int) $site['id']);
        return $this->render('sites/subdomains', [
            'site' => $site, 'subdomains' => $sub, 'tab' => 'subdomains',
            'hasZone' => $zone !== null && $this->dns->zone((int) $zone['id'], $this->user($request)) !== null,
        ]);
    }

    /** Addon domains created from this website (their own page). */
    public function addons(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $children = $this->sites->children($this->server->id(), (int) $site['id']);
        $addon = array_values(array_filter($children, static fn (array $c): bool => !str_ends_with((string) $c['domain'], '.' . $site['domain'])));
        $user = $this->user($request);
        return $this->render('sites/addons', [
            'site' => $site, 'addons' => $addon, 'tab' => 'addons',
            // "Convert to account": administrators and resellers (route permission).
            'plans' => $this->plans->visible($user),
            'moves' => array_combine(array_map(static fn (array $a): int => (int) $a['id'], $addon), array_map(fn (array $a): array => $this->conversion->preview($a), $addon)) ?: [],
        ]);
    }

    /** Security overview for a website, with fix guidance. */
    public function security(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        return $this->render('sites/security', ['site' => $site, 'tab' => 'security', 'checks' => $this->health->security($site)]);
    }

    /** Health overview for a website, with fix guidance. */
    public function health(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        return $this->render('sites/health', ['site' => $site, 'tab' => 'health', 'checks' => $this->health->health($site, $this->user($request))]);
    }

    /** Per-site Memcached: connection details and status (local, shared). */
    public function memcached(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $status = null;
        $error = null;
        try {
            $status = (array) $this->agent->call('memcached.status');
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/memcached', ['site' => $site, 'tab' => 'memcached', 'status' => $status, 'agentError' => $error]);
    }

    public function storeSubdomain(Request $request, string $id): Response
    {
        return $this->storeChild($request, $id, 'subdomain');
    }

    public function storeAddon(Request $request, string $id): Response
    {
        return $this->storeChild($request, $id, 'addon');
    }

    private function storeChild(Request $request, string $id, string $type): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $back = '/sites/' . (int) $id . '/' . ($type === 'subdomain' ? 'subdomains' : 'addons');
        try {
            $res = $this->service->createChild($site, $type, $request->all(), $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors($back . '#new-' . $type, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        $child = $this->sites->find($this->server->id(), $res['id'], null);
        return $this->redirectWithSuccess($back, ($type === 'addon' ? 'Addon domain ' : 'Subdomain ') . ($child['domain'] ?? '') . ' created. Its files are in ' . ($child['document_root'] ?? '') . '.' . ($res['dns'] ? ' A DNS record was added.' : ''));
    }

    /** Server details and the actions that change or remove the website. */
    public function advanced(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        [$inspect, $inspectError] = $this->inspect($site);
        return $this->render('sites/advanced', [
            'site' => $site,
            'inspect' => $inspect,
            'inspectError' => $inspectError,
            'jobs' => $this->jobs->recentFor('website', (string) $site['id']),
            'tab' => 'advanced',
            // Addresses the website may be bound to (administrators).
            'ipPool' => \NimVor\Hosting\Scope::isAll($this->user($request)) ? array_values(array_filter($this->ips->pool(), static fn (array $a): bool => (int) $a['reserved'] === 0
                && ($a['kind'] !== 'dedicated' || $a['sites'] === [] || (int) $a['sites'][0]['id'] === (int) $site['id']))) : null,
        ]);
    }

    /**
     * @param array<string, mixed> $site
     * @return array{0: array<string, mixed>|null, 1: AgentException|null}
     */
    private function inspect(array $site): array
    {
        try {
            return [(array) $this->agent->call('sites.inspect', ['domain' => $site['domain']]), null];
        } catch (AgentException $e) {
            return [null, $e];
        }
    }

    public function undoImport(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        try {
            $this->imports->undo($site);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id, $e);
        }
        return $this->redirectWithSuccess('/websites', 'Import undone: the original configuration serves ' . $site['domain'] . ' again, and the NimVor copy was removed.');
    }

    public function edit(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        return $this->render('sites/form', ['site' => $site, 'phpVersions' => $this->service->phpVersions(), 'owners' => [], 'tab' => 'settings', 'profileDefault' => $this->service->defaultPhpProfile()]);
    }

    public function update(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $input = $request->all();
        try {
            $this->service->update($site, $input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/sites/' . (int) $id . '/settings', $e, $input);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/settings', $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $id, 'Settings saved and applied.');
    }

    public function toggle(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        // The site page's and the list's buttons return there; Advanced's to Advanced.
        $back = match ($request->string('return_to')) {
            'overview' => '/sites/' . (int) $id,
            'websites' => '/websites',
            default => '/sites/' . (int) $id . '/advanced',
        };
        try {
            $this->service->setEnabled($site, !$site['enabled']);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, $site['enabled'] ? 'Website disabled. Its files and settings are kept.' : 'Website enabled.');
    }

    public function restartPhp(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        try {
            $this->service->restartPhp($site);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/advanced', $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $id . '/advanced', 'PHP ' . $site['php_version'] . ' was reloaded.');
    }

    public function directoryListing(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $on = $request->input('enabled') === '1';
        try {
            $this->service->setDirectoryListing($site, $on);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/advanced#files', $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $id . '/advanced#files', $on ? 'Directory listing is on: folders without an index page list their files.' : 'Directory listing is off.');
    }

    /** Checks the website's file permissions (JSON, nothing changed). */
    public function checkPermissions(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return Response::json(['error' => 'Website not found.'], 404);
        }
        try {
            return Response::json($this->service->permissions($site, false));
        } catch (UserFacingException $e) {
            return Response::json(['error' => $e->getMessage()], 502);
        }
    }

    public function repairPermissions(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        try {
            $res = $this->service->permissions($site, true);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/advanced#files', $e);
        }
        $skipped = (int) ($res['skipped_count'] ?? 0);
        return $this->redirectWithSuccess('/sites/' . (int) $id . '/advanced#files', sprintf('Checked %d file(s) and folder(s); repaired %d.', (int) ($res['checked'] ?? 0), (int) ($res['fixed'] ?? 0)) . ($skipped > 0 ? " {$skipped} link(s) or special file(s) were left as they are." : ''));
    }

    public function reapply(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        try {
            $this->service->reapply($site);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/advanced', $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $id . '/advanced', 'Configuration re-applied from the panel.');
    }

    public function destroy(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        if ($request->string('confirm_domain') !== $site['domain']) {
            return $this->redirectWithError('/sites/' . (int) $id . '/advanced', 'Type the domain name to confirm deletion.');
        }
        try {
            $warnings = $this->service->delete($site, $request->input('remove_files') === '1', $request->input('remove_user') === '1');
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/advanced', $e);
        }
        $msg = 'Website ' . $site['domain'] . ' was removed.';
        if ($warnings !== []) {
            $msg .= ' ' . implode(' ', $warnings);
        }
        return $this->redirectWithSuccess('/websites', $msg);
    }
}
