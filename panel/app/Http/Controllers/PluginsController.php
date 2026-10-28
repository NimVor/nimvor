<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Plugins\PluginManager;
use NimVor\Services\AuditLogger;
use NimVor\View\View;

final class PluginsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly PluginManager $plugins,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->render('plugins/index', ['plugins' => $this->plugins->discover(), 'dir' => $this->plugins->directory()]);
    }

    public function toggle(Request $request, string $name): Response
    {
        $enable = $request->input('enabled') === '1';
        try {
            $this->plugins->setEnabled($name, $enable, $this->userId($request));
        } catch (\InvalidArgumentException $e) {
            return $this->redirectWithError('/admin/plugins', $e->getMessage());
        }
        $this->audit->record($enable ? 'plugin.enable' : 'plugin.disable', 'plugin', $name);
        return $this->redirectWithSuccess('/admin/plugins', $enable ? "Plugin {$name} enabled. Its pages appear in the sidebar for users with its permissions." : "Plugin {$name} disabled.");
    }
}
