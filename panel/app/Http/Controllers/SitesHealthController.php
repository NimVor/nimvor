<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\AuditLogger;
use NimVor\Services\SitesHealthService;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/** Websites → Site health: what needs attention across the websites, each linking to its fix. */
final class SitesHealthController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SitesHealthService $health,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $filter = (string) $request->query('category', '');
        $result = $this->health->overview($this->user($request));
        if (isset(SitesHealthService::CATEGORIES[$filter])) {
            $result['sites'] = array_values(array_filter($result['sites'], static fn (array $row): bool => in_array($filter, array_column($row['issues'], 'category'), true)));
        } else {
            $filter = '';
        }
        return $this->render('sites/health_overview', ['result' => $result, 'filter' => $filter, 'checkedAt' => gmdate('Y-m-d H:i')]);
    }

    /** Adds the email records (MX, SPF, DKIM, DMARC) that are missing from a domain's zone on this panel. */
    public function fixEmail(Request $request): Response
    {
        $user = $this->user($request);
        $id = (int) $request->input('domain_id', 0);
        try {
            $mailHost = $this->health->overview($user)['mail_host'];
            $added = $this->health->fixEmail($id, $user, $mailHost);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites-health', $e);
        }
        $this->audit->record('sites.health.fix_email', 'mail_domain', (string) $id, AuditLogger::SUCCESS, ['added' => $added]);
        return $this->redirectWithSuccess('/sites-health', $added > 0 ? $added . ' email record(s) added to the DNS zone.' : 'Nothing to add: the missing records are not ones this panel can publish.');
    }
}
