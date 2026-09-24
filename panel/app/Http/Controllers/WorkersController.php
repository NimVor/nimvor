<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\ServerContext;
use WhaCP\Services\WorkerService;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

final class WorkersController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly WorkerService $workers,
    ) {
        parent::__construct($view, $session, $sites, $server);
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
