<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\AppCatalogService;
use NimVor\Services\AuditLogger;
use NimVor\Services\ServerContext;
use NimVor\View\View;

/**
 * Administration → Application Manager: control the Softaculous-style
 * installer. Turn the whole installer on or off, enable or disable whole
 * categories, and per application toggle whether it is enabled at all and
 * whether it is visible to customers. Administrators only (settings.manage).
 */
final class AppManagerController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AppCatalogService $catalog,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->render('server/app_manager', [
            'tab' => 'apps', 'categories' => $this->catalog->forAdmin(), 'installerEnabled' => $this->catalog->installerEnabled(),
        ]);
    }

    public function installer(Request $request): Response
    {
        $on = $request->input('on') === '1';
        $this->catalog->setInstallerEnabled($on, $this->userId($request));
        $this->audit->record('apps.installer', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['enabled' => $on], $this->userId($request));
        return $this->redirectWithSuccess('/apps-manager', 'Application installer ' . ($on ? 'enabled' : 'disabled') . '.');
    }

    public function category(Request $request): Response
    {
        $slug = (string) $request->input('slug');
        $on = $request->input('on') === '1';
        $this->catalog->setCategoryEnabled($slug, $on);
        $this->audit->record('apps.category', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['category' => $slug, 'enabled' => $on], $this->userId($request));
        return $this->redirectWithSuccess('/apps-manager', 'Category ' . ($on ? 'enabled' : 'disabled') . '.');
    }

    public function app(Request $request): Response
    {
        $slug = (string) $request->input('slug');
        $field = $request->input('field') === 'customer_visible' ? 'customer_visible' : 'enabled';
        $on = $request->input('on') === '1';
        if ($this->catalog->find($slug) === null) {
            return $this->redirectWithError('/apps-manager', 'Unknown application.');
        }
        $this->catalog->setAppFlag($slug, $field, $on);
        $this->audit->record('apps.app', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['app' => $slug, 'field' => $field, 'on' => $on], $this->userId($request));
        return $this->redirectWithSuccess('/apps-manager', 'Application updated.');
    }
}
