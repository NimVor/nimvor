<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\AddonConversionService;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/** Subdomains & addons → "Convert to account" (administrators and resellers). */
final class AddonConversionController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly AddonConversionService $conversion,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    public function convert(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $back = '/sites/' . (int) ($site['parent_id'] ?? $site['id']) . '/domains';
        $input = $request->all();
        try {
            $res = $this->conversion->convert($site, $input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors($back . '#convert-' . (int) $site['id'], $e, array_diff_key($input, ['password' => 1, 'password_confirmation' => 1]) + ['convert' => (string) (int) $site['id']]);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        } catch (AgentException $e) {
            return $this->redirectWithError($back, $site['domain'] . ' was not converted; everything was put back as it was. ' . $e->getMessage());
        }
        return $this->redirectWithSuccess('/users/' . $res['id'] . '/edit', $site['domain'] . ' is now the hosting account ' . $res['username'] . ', with its files, databases, DNS zone and email.');
    }
}
