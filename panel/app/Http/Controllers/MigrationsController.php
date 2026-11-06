<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\DatabaseRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\MigrationService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Migrations: move websites and databases from another server into NimVor,
 * step by step. Administrators only: a migration writes into websites and
 * databases and connects to another server with its login.
 */
final class MigrationsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MigrationService $service,
        private readonly WebsiteRepository $sites,
        private readonly DatabaseRepository $dbs,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->redirectWithError('/', 'Only administrators can migrate websites.');
    }

    public function index(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        foreach ($this->service->list() as $m) {
            if (in_array($m['status'], ['running', 'discovering'], true)) {
                $this->service->refresh($m['id']);
            }
        }
        return $this->render('migrations/index', ['migrations' => $this->service->list()]);
    }

    public function sources(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        return $this->render('migrations/new', ['sources' => $this->service->sources()]);
    }

    public function store(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $id = $this->service->create((string) $request->input('panel', ''), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/migrations/new', $e);
        }
        return Response::redirect('/migrations/' . $id);
    }

    public function show(Request $request, string $id): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $mid = (int) $id;
        $this->service->refresh($mid);
        $m = $this->service->find($mid);
        if ($m === null) {
            return $this->redirectWithError('/migrations', 'Migration not found.');
        }
        $step = $this->stepFor($m, (string) $request->input('step', ''));
        $user = ['role_scope' => 'all', 'id' => $this->userId($request)];
        return $this->render('migrations/show', [
            'm' => $m,
            'step' => $step,
            'state' => in_array($step, ['run', 'report'], true) ? $this->service->state($m) : [],
            'sites' => $step === 'select' ? $this->sites->all($this->server->id(), $user) : [],
            'databases' => $step === 'select' ? $this->dbs->databases($this->server->id(), $user) : [],
            'errors' => (array) ($this->session->pullFlash('errors') ?? []),
            'old' => (array) ($this->session->pullFlash('old') ?? []),
        ]);
    }

    /** The step a migration is on; an earlier step can be opened with ?step= while nothing is running. */
    private function stepFor(array $m, string $want): string
    {
        $natural = match ($m['status']) {
            'draft' => 'connect',
            'connected', 'discovering' => 'discover',
            'discovered' => 'select',
            'selected', 'assessed' => 'assess',
            'completed' => 'report',
            default => 'run',
        };
        $order = ['connect' => 1, 'discover' => 2, 'select' => 3, 'assess' => 4];
        if (isset($order[$want], $order[$natural]) && $order[$want] <= $order[$natural] && !($want === 'discover' && !$m['has_login']) && ($want === 'connect' || $m['has_login'])) {
            return $want;
        }
        return $natural;
    }

    private function act(Request $request, string $id, callable $fn, string $success, string $to = ''): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $to = $to !== '' ? $to : '/migrations/' . (int) $id;
        try {
            $fn((int) $id);
        } catch (ValidationException $e) {
            return $this->backWithErrors($to, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        return $this->redirectWithSuccess($to, $success);
    }

    public function probe(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->probe($i, $request->all()), 'Check the SSH key below, then enter the login.');
    }

    public function connect(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->connect($i, $request->all()), 'Connected. The source was read; nothing was changed on it.');
    }

    public function discover(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (int $i): void {
            $this->flashJob($this->service->discover($i));
        }, 'Discovery started. It only reads the source.');
    }

    public function select(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->select($i, $request->all()), 'Your choices are saved. Run the compatibility check next.');
    }

    public function assess(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->assess($i), 'The compatibility check is done.');
    }

    public function start(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (int $i): void {
            $this->flashJob($this->service->begin($i));
        }, 'The migration has started. It runs in the background; you can close this page.');
    }

    public function resume(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (int $i): void {
            $this->flashJob($this->service->resume($i));
        }, 'The migration continues. Tasks that are done are not repeated.');
    }

    public function pause(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->pause($i), 'The migration will pause at the next safe point.');
    }

    public function cancel(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->cancel($i), 'The migration was cancelled. What was copied stays; nothing was removed from the source.');
    }

    public function rollback(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->rollback($i, $request->input('confirm') === '1', $request->input('empty_databases') === '1'), 'The rollback is done. What was kept is listed below.');
    }

    public function forget(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->forgetLogin($i), 'The stored login was erased.');
    }

    public function delete(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (int $i) => $this->service->delete($i), 'The migration record was removed.', '/migrations');
    }

    public function report(Request $request, string $id): Response
    {
        if ($this->denied($request)) {
            return $this->redirectWithError('/', 'Only administrators can read migration reports.');
        }
        try {
            $report = $this->service->report((int) $id);
        } catch (UserFacingException) {
            return $this->redirectWithError('/migrations', 'Migration not found.');
        }
        return new Response((string) json_encode($report, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE), 200, [
            'Content-Type' => 'application/json; charset=utf-8',
            'Content-Disposition' => 'attachment; filename="migration-' . (int) $id . '-report.json"',
            'Cache-Control' => 'no-store',
        ]);
    }
}
