<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\ServerContext;
use WhaCP\Services\SettingsService;
use WhaCP\Services\SslService;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/**
 * SSL/TLS for server administrators: SSL hosts, the certificate store,
 * private keys and CSRs made on the server, and the server-wide TLS
 * configuration. Customers use each website's SSL page.
 */
final class SslCenterController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly WebsiteRepository $websites,
        private readonly ServerContext $server,
        private readonly SslService $ssl,
        private readonly SettingsService $settings,
        private readonly \WhaCP\Services\AutoSslService $autoSsl,
        private readonly \WhaCP\Services\JobTracker $jobs,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'The SSL/TLS section is for server administrators. Each website has its own SSL page.', 'status' => 403], 403);
    }

    /** @param array<string, mixed> $data */
    private function page(string $tab, array $data, ?AgentException $error = null): Response
    {
        return $this->render('ssl/' . $tab, $data + ['tab' => $tab, 'agentError' => $error, 'warnDays' => $this->settings->int('ssl_warning_days')]);
    }

    /** @return array<string, mixed>|null */
    private function call(string $action, array $params, ?AgentException &$error): ?array
    {
        try {
            return json_decode((string) json_encode($this->agent->call($action, $params, 60)), true);
        } catch (AgentException $e) {
            $error = $e;
            return null;
        }
    }

    /** Websites by host name (domain and aliases). @return array<string, array<string, mixed>> */
    private function sitesByName(Request $request): array
    {
        $out = [];
        foreach ($this->websites->all($this->server->id(), $this->user($request)) as $w) {
            foreach (array_merge([(string) $w['domain']], (array) ($w['aliases'] ?? [])) as $n) {
                $out[strtolower((string) $n)] = $w;
            }
        }
        return $out;
    }

    public function hosts(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $error = null;
        $store = $this->call('ssl.store', [], $error);
        $renewal = $error === null ? $this->call('ssl.renewal_status', [], $error) : null;
        return $this->page('hosts', ['hosts' => (array) ($store['hosts'] ?? []), 'sites' => $this->sitesByName($request), 'renewal' => $renewal, 'autoSsl' => $this->settings->bool('auto_ssl')], $error);
    }

    /** Turns on certbot's renewal timer (EPEL's package leaves it off). */
    public function enableRenewal(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $st = (array) $this->agent->call('ssl.renewal_enable', [], 60);
        } catch (UserFacingException $e) {
            $this->audit->record('ssl.renewal_enable', 'server', 'certbot', AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/ssl/autossl', $e);
        }
        $this->audit->record('ssl.renewal_enable', 'server', 'certbot', AuditLogger::SUCCESS, ['method' => (string) ($st['method'] ?? '')]);
        return $this->redirectWithSuccess('/ssl/autossl', 'Automatic renewal of Let\'s Encrypt certificates is on (' . (string) ($st['method'] ?? '') . ').');
    }

    public function certificates(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $error = null;
        $store = $this->call('ssl.store', [], $error);
        return $this->page('certificates', ['certificates' => (array) ($store['certificates'] ?? [])], $error);
    }

    public function deleteCertificate(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $source = $request->string('source');
        $name = $request->string('name');
        try {
            match ($source) {
                'letsencrypt' => $this->agent->call('ssl.delete', ['cert_name' => $name], 60),
                'whacp' => $this->agent->call('ssl.custom_delete', ['name' => $name], 60),
                default => throw new UserFacingException('Only Let\'s Encrypt and WhaCP certificates can be deleted here.'),
            };
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/ssl/certificates', $e);
        }
        $this->audit->record('ssl.certificate_delete', 'server', 'ssl', AuditLogger::SUCCESS, ['source' => $source, 'name' => $name]);
        return $this->redirectWithSuccess('/ssl/certificates', 'Certificate ' . $name . ' deleted.');
    }

    public function keys(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $error = null;
        $list = $this->call('ssl.keys', [], $error);
        $id = (string) $request->query('id', '');
        $key = $id !== '' && $error === null ? $this->call('ssl.key', ['id' => $id], $error) : null;
        $sites = $this->websites->all($this->server->id(), $this->user($request));
        return $this->page('keys', ['keys' => (array) ($list['keys'] ?? []), 'keyTypes' => (array) ($list['key_types'] ?? []), 'key' => $key, 'websites' => $sites], $error);
    }

    public function createCsr(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $names = array_values(array_filter(array_map(static fn (string $n): string => strtolower(trim($n)), preg_split('/[\s,]+/', $request->string('names')) ?: [])));
        $params = [
            'names' => $names, 'organization' => $request->string('organization'), 'organizational_unit' => $request->string('organizational_unit'),
            'locality' => $request->string('locality'), 'province' => $request->string('province'), 'country' => strtoupper($request->string('country')),
            'email' => $request->string('email'), 'key_type' => $request->string('key_type'),
            'self_signed_days' => $request->input('self_signed') === '1' ? max(1, min(825, (int) $request->input('self_signed_days', 365))) : 0,
        ];
        try {
            $res = (array) $this->agent->call('ssl.csr_create', $params, 120);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/ssl/keys', $e);
        }
        $this->audit->record('ssl.csr_create', 'server', 'ssl', AuditLogger::SUCCESS, ['id' => $res['id'] ?? '', 'names' => $names, 'key_type' => $params['key_type'], 'self_signed' => $params['self_signed_days'] > 0]);
        return $this->redirectWithSuccess('/ssl/keys?id=' . rawurlencode((string) ($res['id'] ?? '')), 'Key and CSR created. Send the CSR to your certificate authority; the private key stays on this server.');
    }

    public function installKey(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $id = $request->string('key_id');
        $back = '/ssl/keys?id=' . rawurlencode($id);
        $site = $this->websites->find($this->server->id(), (int) $request->input('website_id', 0), $this->user($request));
        if ($site === null) {
            return $this->redirectWithError($back, 'Choose a website.');
        }
        try {
            $this->ssl->installFromKey($site, $id, (string) $request->input('certificate', ''), (string) $request->input('chain', ''), $request->input('self_signed') === '1');
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $site['id'] . '/ssl', 'Certificate installed on ' . $site['domain'] . ' and HTTPS enabled.');
    }

    public function deleteKey(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $id = $request->string('id');
        try {
            $this->agent->call('ssl.key_delete', ['id' => $id], 60);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/ssl/keys', $e);
        }
        $this->audit->record('ssl.key_delete', 'server', 'ssl', AuditLogger::SUCCESS, ['id' => $id]);
        return $this->redirectWithSuccess('/ssl/keys', 'Key ' . $id . ' deleted.');
    }

    public function csrDownload(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $error = null;
        $key = $this->call('ssl.key', ['id' => (string) $request->query('id', '')], $error);
        if ($key === null || empty($key['csr'])) {
            return $this->redirectWithError('/ssl/keys', 'That CSR does not exist.');
        }
        $file = preg_replace('/[^a-z0-9.-]/', '_', (string) $key['id']) . '.csr';
        return new Response((string) $key['csr'], 200, ['Content-Type' => 'application/pkcs10', 'Content-Disposition' => 'attachment; filename="' . $file . '"', 'X-Content-Type-Options' => 'nosniff']);
    }

    public function tls(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $error = null;
        $tls = $this->call('ssl.tls_get', [], $error);
        return $this->page('tls', ['tls' => $tls], $error);
    }

    public function saveTls(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $profile = ['protocols' => $request->string('protocols'), 'ciphers' => $request->string('ciphers'), 'session_tickets' => $request->input('session_tickets') === '1'];
        try {
            $res = (array) $this->agent->call('ssl.tls_set', $profile, 120);
        } catch (UserFacingException $e) {
            $this->audit->record('ssl.tls', 'server', 'nginx', AuditLogger::FAILURE, $profile + ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/ssl/tls', $e);
        }
        $this->audit->record('ssl.tls', 'server', 'nginx', AuditLogger::SUCCESS, $profile);
        $moved = (int) ($res['migrated_vhosts'] ?? 0);
        return $this->redirectWithSuccess('/ssl/tls', 'TLS configuration applied and Nginx reloaded.' . ($moved > 0 ? ' ' . $moved . ' website configuration(s) now use it.' : ''));
    }

    /** Certificates and TLS of the panel, the mail server and the host name. */
    public function services(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $error = null;
        $svc = $this->call('ssl.services', [], $error);
        return $this->page('services', ['svc' => $svc], $error);
    }

    public function serviceCert(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $service = $request->string('service');
        $name = $request->string('cert_name');
        try {
            $this->agent->call('ssl.service_cert', ['service' => $service, 'cert_name' => $name], 120);
        } catch (UserFacingException $e) {
            $this->audit->record('ssl.service_cert', 'server', $service, AuditLogger::FAILURE, ['cert_name' => $name, 'error' => $e->getMessage()]);
            return $this->redirectWithFailure('/ssl/services', $e);
        }
        $this->audit->record('ssl.service_cert', 'server', $service, AuditLogger::SUCCESS, ['cert_name' => $name]);
        $label = $service === 'panel' ? 'The panel' : 'The mail server';
        return $this->redirectWithSuccess('/ssl/services', $name === '' ? $label . ' uses its previous certificate again.' : $label . ' now uses the certificate ' . $name . '.' . ($service === 'panel' ? ' Reload the page if the browser still shows the old one.' : ''));
    }

    public function mailTls(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $profile = ['protocols' => $request->string('protocols'), 'ciphers' => $request->string('ciphers')];
        try {
            $this->agent->call('ssl.mail_tls', $profile, 120);
        } catch (UserFacingException $e) {
            $this->audit->record('ssl.mail_tls', 'server', 'mail', AuditLogger::FAILURE, $profile + ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/ssl/services', $e);
        }
        $this->audit->record('ssl.mail_tls', 'server', 'mail', AuditLogger::SUCCESS, $profile);
        return $this->redirectWithSuccess('/ssl/services', 'The mail server\'s TLS configuration was applied; Postfix and Dovecot were reloaded.');
    }

    public function hostnameIssue(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        // Expiry notices go to the administrator who asked.
        $email = (string) ($this->user($request)['email'] ?? '');
        $email = filter_var($email, FILTER_VALIDATE_EMAIL) !== false ? $email : '';
        try {
            $res = (array) $this->agent->call('ssl.hostname_issue', ['email' => $email], 60);
        } catch (UserFacingException $e) {
            $this->audit->record('ssl.hostname_issue', 'server', 'hostname', AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/ssl/services', $e);
        }
        $this->jobs->track((string) $res['job_id'], 'Let\'s Encrypt for ' . (string) ($res['hostname'] ?? 'the host name'), 'server', (string) $this->server->id(), $this->userId($request));
        $this->audit->record('ssl.hostname_issue', 'server', 'hostname', AuditLogger::SUCCESS, ['hostname' => $res['hostname'] ?? '']);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess('/ssl/services', 'Asking Let\'s Encrypt for a certificate for ' . (string) ($res['hostname'] ?? 'the host name') . '; the window shows its progress. When it is issued, choose it for the panel and the mail server below.');
    }

    /** AutoSSL: provider, settings, next check, queue, log, certbot's renewals. */
    public function autossl(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $error = null;
        $renewal = $this->call('ssl.renewal_status', [], $error);
        $renewLog = $error === null ? $this->call('ssl.renewal_log', [], $error) : null;
        $last = $this->autoSsl->lastSchedulerRun();
        $queued = $this->autoSsl->queued();
        return $this->page('autossl', [
            'renewal' => $renewal,
            'renewLog' => (array) ($renewLog['lines'] ?? []),
            'log' => $this->autoSsl->recentLog(300),
            'queued' => $queued,
            'nextCheck' => $last !== null ? $last + 300 : null,
            'autoNew' => $this->settings->bool('auto_ssl'),
            'autoReplace' => $this->settings->bool('auto_ssl_replace'),
        ], $error);
    }

    /** Run AutoSSL for all websites now: queue them, then work through the first few. */
    public function autosslRun(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $n = $this->autoSsl->queue(true);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/ssl/autossl', $e);
        }
        $this->audit->record('ssl.autossl_run', 'server', 'autossl', AuditLogger::SUCCESS, ['queued' => $n]);
        if ($n === 0) {
            return $this->redirectWithSuccess('/ssl/autossl', 'Every website already has a valid certificate; nothing to do.');
        }
        // The first ones now (each can take up to a minute), the rest with
        // the scheduler every 5 minutes.
        @set_time_limit(300);
        $done = $this->autoSsl->process(3);
        return $this->redirectWithSuccess('/ssl/autossl', $n . ' website(s) queued; ' . count($done) . ' handled now, the rest within the next scheduler runs (every 5 minutes). See the log below.');
    }
}
