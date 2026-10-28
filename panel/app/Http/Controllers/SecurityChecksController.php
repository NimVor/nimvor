<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Security\Permissions as P;
use NimVor\Services\AuditLogger;
use NimVor\Services\MailService;
use NimVor\Services\ServerContext;
use NimVor\View\View;

/**
 * On-demand security checks that are too slow to run on every page load:
 * file integrity (dpkg/rpm verification), the TLS configuration audit and
 * OS vulnerability advisories. Each page opens instantly with a Run button;
 * running calls the agent synchronously and shows the findings. The checks
 * are read-only; only recording the file-integrity baseline changes state.
 */
final class SecurityChecksController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly MailService $mail,
    ) {
        parent::__construct($view, $session);
    }

    /** @param array<string, mixed> $data */
    private function page(string $tab, array $data): Response
    {
        return $this->render('security/' . $tab, $data + ['tab' => $tab, 'pending' => null, 'agentError' => null]);
    }

    // ---- File integrity (items 104–106) ----------------------------------

    public function integrity(Request $request): Response
    {
        return $this->page('integrity', ['result' => null, 'ran' => false]);
    }

    public function runIntegrity(Request $request): Response
    {
        try {
            $result = (array) $this->agent->call('security.integrity', [], 180);
        } catch (AgentException $e) {
            return $this->page('integrity', ['result' => null, 'ran' => true, 'error' => $e->getMessage()]);
        }
        $this->audit->record('security.integrity', 'server', 'integrity', AuditLogger::SUCCESS, ['findings' => count((array) ($result['findings'] ?? []))]);
        return $this->page('integrity', ['result' => $result, 'ran' => true]);
    }

    public function setBaseline(Request $request): Response
    {
        try {
            $res = (array) $this->agent->call('security.integrity_baseline', [], 60);
        } catch (AgentException $e) {
            return $this->redirectWithError('/security/integrity', 'The baseline could not be recorded: ' . $e->getMessage());
        }
        $this->audit->record('security.integrity_baseline', 'server', 'integrity', AuditLogger::SUCCESS, ['files' => (int) ($res['baselined'] ?? 0)]);
        return $this->redirectWithSuccess('/security/integrity', 'Integrity baseline recorded for ' . (int) ($res['baselined'] ?? 0) . ' NimVor-owned file(s). Run the check after an update to refresh it.');
    }

    // ---- TLS configuration audit (items 70–71) ---------------------------

    public function tls(Request $request): Response
    {
        return $this->page('tls', ['result' => null, 'ran' => false, 'endpoints' => count($this->tlsEndpoints())]);
    }

    public function runTls(Request $request): Response
    {
        $eps = $this->tlsEndpoints();
        if ($eps === []) {
            return $this->page('tls', ['result' => null, 'ran' => true, 'endpoints' => 0]);
        }
        try {
            $result = (array) $this->agent->call('tls.audit', ['endpoints' => $eps], 180);
        } catch (AgentException $e) {
            return $this->page('tls', ['result' => null, 'ran' => true, 'endpoints' => count($eps), 'error' => $e->getMessage()]);
        }
        $this->audit->record('tls.audit', 'server', 'tls', AuditLogger::SUCCESS, ['endpoints' => count($eps)]);
        return $this->page('tls', ['result' => $result, 'ran' => true, 'endpoints' => count($eps)]);
    }

    /**
     * The server's own TLS endpoints: every enabled HTTPS website (dialed on
     * loopback with its name for SNI) and the mail server when it is set up.
     *
     * @return list<array<string, mixed>>
     */
    private function tlsEndpoints(): array
    {
        $eps = [];
        $rows = $this->db->fetchAll('SELECT domain FROM websites WHERE server_id = ? AND enabled = 1 AND https = 1 ORDER BY domain LIMIT 200', [$this->server->id()]);
        foreach ($rows as $r) {
            $eps[] = ['port' => 443, 'server_name' => (string) $r['domain'], 'kind' => 'website'];
        }
        $mail = $this->mail->status();
        $host = (string) ($mail['hostname'] ?? '');
        if ($mail !== null && !empty($mail['configured']) && $host !== '') {
            $eps[] = ['port' => 465, 'server_name' => $host, 'kind' => 'mail-smtps'];
            $eps[] = ['port' => 587, 'server_name' => $host, 'kind' => 'mail-submission', 'starttls' => 'smtp'];
            $eps[] = ['port' => 993, 'server_name' => $host, 'kind' => 'mail-imaps'];
        }
        return $eps;
    }

    // ---- OS vulnerability advisories (item 164) --------------------------

    public function advisories(Request $request): Response
    {
        return $this->page('advisories', ['result' => null, 'ran' => false]);
    }

    public function runAdvisories(Request $request): Response
    {
        try {
            $result = (array) $this->agent->call('packages.advisories', [], 120);
        } catch (AgentException $e) {
            return $this->page('advisories', ['result' => null, 'ran' => true, 'error' => $e->getMessage()]);
        }
        return $this->page('advisories', ['result' => $result, 'ran' => true]);
    }
}
