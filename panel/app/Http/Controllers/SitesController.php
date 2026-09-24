<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\UserRepository;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Services\WebsiteService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/**
 * WhaCP-managed websites (create, settings, enable/disable, delete).
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
        private readonly \WhaCP\Services\ImportService $imports,
        private readonly JobTracker $jobs,
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
        return $this->render('sites/form', [
            'site' => null,
            'phpVersions' => $this->service->phpVersions(),
            'owners' => \WhaCP\Hosting\Scope::isAll($this->user($request)) ? $this->users->all() : [],
        ]);
    }

    public function store(Request $request): Response
    {
        $input = $request->all();
        try {
            $id = $this->service->create($input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/websites/new', $e, $input);
        } catch (UserFacingException $e) {
            $this->session->flash('old', array_diff_key($input, ['_token' => 1]));
            return $this->redirectWithFailure('/websites/new', $e);
        }
        return $this->redirectWithSuccess('/sites/' . $id, 'Website created. Nginx and PHP-FPM were validated and reloaded.');
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
        $inspect = null;
        $inspectError = null;
        try {
            $inspect = (array) $this->agent->call('sites.inspect', ['domain' => $site['domain']]);
        } catch (AgentException $e) {
            $inspectError = $e;
        }
        return $this->render('sites/show', [
            'site' => $site,
            'inspect' => $inspect,
            'inspectError' => $inspectError,
            'jobs' => $this->jobs->recentFor('website', (string) $site['id']),
            'tab' => 'overview',
            'import' => $this->imports->importOf((int) $site['id']),
        ]);
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
        return $this->redirectWithSuccess('/websites', 'Import undone: the original configuration serves ' . $site['domain'] . ' again, and the WhaCP copy was removed.');
    }

    public function edit(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        return $this->render('sites/form', ['site' => $site, 'phpVersions' => $this->service->phpVersions(), 'owners' => [], 'tab' => 'settings']);
    }

    public function update(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $input = $request->all();
        try {
            $this->service->update($site, $input);
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
        try {
            $this->service->setEnabled($site, !$site['enabled']);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id, $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $id, $site['enabled'] ? 'Website disabled. Its files and settings are kept.' : 'Website enabled.');
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
            return $this->redirectWithFailure('/sites/' . (int) $id, $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $id, 'PHP ' . $site['php_version'] . ' was reloaded.');
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
            return $this->redirectWithFailure('/sites/' . (int) $id, $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $id, 'Configuration re-applied from the panel.');
    }

    public function destroy(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        if ($request->string('confirm_domain') !== $site['domain']) {
            return $this->redirectWithError('/sites/' . (int) $id, 'Type the domain name to confirm deletion.');
        }
        try {
            $warnings = $this->service->delete($site, $request->input('remove_files') === '1', $request->input('remove_user') === '1');
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id, $e);
        }
        $msg = 'Website ' . $site['domain'] . ' was removed.';
        if ($warnings !== []) {
            $msg .= ' ' . implode(' ', $warnings);
        }
        return $this->redirectWithSuccess('/websites', $msg);
    }
}
