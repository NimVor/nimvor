<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\ServerContext;
use WhaCP\Services\SettingsService;
use WhaCP\Services\SslService;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

final class SslController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly WebsiteRepository $sites,
        private readonly SslService $ssl,
        private readonly ServerContext $server,
        private readonly SettingsService $settings,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    private function site(Request $request, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($request));
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        return $this->render('sites/ssl', [
            'site' => $site,
            'tab' => 'ssl',
            'cert' => $this->ssl->certificate($site),
            'renewal' => $this->ssl->renewalStatus(),
            'warnDays' => $this->settings->int('ssl_warning_days'),
            'defaultEmail' => (string) ($this->user($request)['email'] ?? ''),
        ]);
    }

    /** @param callable(array<string, mixed>): string $fn */
    private function act(Request $request, string $id, callable $fn): Response
    {
        $site = $this->site($request, $id);
        $to = '/sites/' . (int) $id . '/ssl';
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        try {
            $msg = $fn($site);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        return $this->redirectWithSuccess($to, $msg);
    }

    public function issue(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (array $site) use ($request): string {
            // Expiry notices go to the account's own email address (none if it is not valid).
            $email = (string) ($this->user($request)['email'] ?? '');
            $this->ssl->issue($site, filter_var($email, FILTER_VALIDATE_EMAIL) !== false ? $email : '', $request->input('include_aliases', '1') === '1', $request->input('staging') === '1');
            return 'Certificate issued. The website now uses HTTPS and HTTP requests are redirected.';
        });
    }

    public function custom(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (array $site) use ($request): string {
            $this->ssl->installCustom($site, (string) $request->input('certificate', ''), (string) $request->input('private_key', ''), (string) $request->input('chain', ''));
            return 'Certificate installed and HTTPS enabled.';
        });
    }

    public function renew(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (array $site): string {
            $this->ssl->renew($site);
            return 'Certificate renewed and Nginx reloaded.';
        });
    }

    public function disable(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (array $site) use ($request): string {
            $this->ssl->disable($site, $request->input('delete_certificate') === '1');
            return 'HTTPS disabled for this website.';
        });
    }

    public function securityHeaders(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (array $site) use ($request): string {
            $on = $request->input('security_headers') === '1';
            $this->ssl->securityHeaders($site, $on);
            return $on ? 'Security headers are on.' : 'Security headers are off.';
        });
    }

    public function options(Request $request, string $id): Response
    {
        return $this->act($request, $id, function (array $site) use ($request): string {
            $this->ssl->options($site, $request->input('force_https') === '1', $request->input('hsts') === '1');
            return 'HTTPS options saved and applied.';
        });
    }
}
