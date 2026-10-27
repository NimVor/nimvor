<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\WorkerService;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

final class WorkersController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly WorkerService $workers,
        private readonly \NimVor\Services\RuntimeService $runtimes,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    /** All workers on the server the user can see. */
    public function index(Request $request): Response
    {
        $rows = [];
        foreach ($this->sites->all($this->server->id(), $this->user($request)) as $site) {
            foreach ($this->workers->forSite($site) as $w) {
                $rows[] = $w + ['domain' => $site['domain'], 'website_id' => (int) $site['id']];
            }
        }
        return $this->render('workers/index', ['workers' => $rows, 'status' => $this->workers->status($rows)]);
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $workers = $this->workers->forSite($site);
        $editId = (int) $request->query('edit', 0);
        return $this->render('sites/workers', [
            'site' => $site,
            'workers' => $workers,
            'status' => $this->workers->status($workers),
            'edit' => $editId > 0 ? $this->workers->find($site, $editId) : null,
            'runtimes' => $this->runtimes->selectable(),
        ]);
    }

    public function store(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'workers', function (array $site) use ($request): string {
            $wid = (int) $request->input('worker_id', 0);
            $this->workers->save($site, $wid > 0 ? $wid : null, $request->all());
            return $wid > 0 ? 'Worker updated and restarted.' : 'Worker created.';
        });
    }

    /** @param callable(array<string, mixed>, array<string, mixed>): string $fn */
    private function withWorker(Request $request, string $id, string $worker, callable $fn): Response
    {
        return $this->act($request, $id, 'workers', function (array $site) use ($worker, $fn): string {
            $w = $this->workers->find($site, (int) $worker) ?? throw new UserFacingException('Worker not found.');
            return $fn($site, $w);
        });
    }

    public function toggle(Request $request, string $id, string $worker): Response
    {
        return $this->withWorker($request, $id, $worker, function (array $site, array $w): string {
            $this->workers->setEnabled($site, $w, !$w['enabled']);
            return $w['enabled'] ? 'Worker stopped.' : 'Worker started.';
        });
    }

    public function restart(Request $request, string $id, string $worker): Response
    {
        return $this->withWorker($request, $id, $worker, function (array $site, array $w): string {
            $this->workers->restart($site, $w);
            return 'Worker restarted.';
        });
    }

    public function delete(Request $request, string $id, string $worker): Response
    {
        return $this->withWorker($request, $id, $worker, function (array $site, array $w): string {
            $this->workers->delete($site, $w);
            return 'Worker removed.';
        });
    }
}
