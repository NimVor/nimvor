<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\JobTracker;
use WhaCP\Services\MailService;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class MailController extends Controller
{
    public const PACKAGES = ['postfix', 'dovecot-imapd', 'dovecot-lmtpd', 'dovecot-sieve', 'opendkim'];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MailService $mail,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        return $this->render('mail/index', [
            'status' => $this->mail->status(), 'domains' => $this->mail->domains($user),
            'websites' => $this->sites->all($this->server->id(), $user), 'admin' => Scope::isAll($user),
            'preselect' => (int) $request->query('website', 0),
        ]);
    }

    /** @param callable(): string $fn */
    private function act(Request $request, string $to, callable $fn): Response
    {
        try {
            return $this->redirectWithSuccess($to, $fn());
        } catch (ValidationException $e) {
            return $this->backWithErrors($to, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }

    public function install(Request $request): Response
    {
        try {
            $res = (array) $this->agent->call('packages.install', ['names' => self::PACKAGES]);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail', $e);
        }
        $this->jobs->track((string) $res['job_id'], 'Install the mail server', 'server', 'mail', $this->userId($request));
        $this->audit->record('packages.install', 'server', 'mail', AuditLogger::SUCCESS, ['names' => self::PACKAGES]);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess('/mail', 'Installing Postfix, Dovecot and OpenDKIM. Then set up email below.');
    }

    public function setup(Request $request): Response
    {
        return $this->act($request, '/mail', function () use ($request): string {
            $host = strtolower(trim($request->string('hostname'), " ."));
            if (!preg_match('/^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/', $host)) {
                throw new ValidationException(['hostname' => 'Enter the mail server\'s full host name, e.g. mail.example.com.']);
            }
            $res = (array) $this->agent->call('mail.configure', ['hostname' => $host, 'confirm_edit_package_config' => $request->input('confirm') === '1'], 180);
            $this->audit->record('mail.configure', 'server', 'mail', AuditLogger::SUCCESS, ['hostname' => $host]);
            return 'Email is set up. ' . implode(' ', array_map('strval', (array) ($res['notes'] ?? [])));
        });
    }

    public function installSpam(Request $request): Response
    {
        try {
            $res = (array) $this->agent->call('packages.install', ['name' => 'rspamd']);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail', $e);
        }
        $this->jobs->track((string) $res['job_id'], 'Install the spam filter', 'server', 'mail', $this->userId($request));
        $this->audit->record('packages.install', 'server', 'mail', AuditLogger::SUCCESS, ['name' => 'rspamd']);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess('/mail', 'Installing Rspamd. Then turn the spam filter on below.');
    }

    public function spam(Request $request): Response
    {
        return $this->act($request, '/mail', function () use ($request): string {
            $mark = (float) $request->input('add_header', 6);
            $reject = (float) $request->input('reject', 15);
            if ($mark < 1 || $mark > 50 || $reject <= $mark || $reject > 100) {
                throw new ValidationException(['add_header' => 'Use a marking score of 1–50 and a higher rejection score (at most 100).']);
            }
            $params = ['enabled' => $request->input('enabled') === '1', 'add_header' => $mark, 'reject' => $reject, 'confirm_replace' => $request->input('confirm_replace') === '1'];
            $this->agent->call('mail.spam_configure', $params, 120);
            $this->audit->record('mail.spam_configure', 'server', 'mail', AuditLogger::SUCCESS, $params);
            return $params['enabled'] ? 'The spam filter is on.' : 'The spam filter is off.';
        });
    }

    public function store(Request $request): Response
    {
        $id = 0;
        $res = $this->act($request, '/mail', function () use ($request, &$id): string {
            $id = $this->mail->addDomain($request->all(), $this->user($request));
            return 'Email is hosted for the domain. Publish the DNS records below.';
        });
        return $id > 0 ? Response::redirect('/mail/domains/' . $id) : $res;
    }

    public function show(Request $request, string $id): Response
    {
        $domain = $this->mail->domain((int) $id, $this->user($request));
        if ($domain === null) {
            return $this->render('errors/error', ['title' => 'Mail domain not found', 'message' => 'This mail domain does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $status = $this->mail->status();
        $sizes = [];
        foreach ((array) ($status['mailboxes'] ?? []) as $m) {
            $sizes[(string) ((array) $m)['address']] = (int) ((array) $m)['bytes'];
        }
        $host = (string) ($status['hostname'] ?? '');
        return $this->render('mail/domain', [
            'domain' => $domain, 'mailboxes' => $this->mail->mailboxes((int) $domain['id']), 'aliases' => $this->mail->aliases((int) $domain['id']),
            'records' => $this->mail->dnsRecords($domain, $host), 'host' => $host, 'sizes' => $sizes,
            'secret' => $this->session->pullFlash('secret'), 'edit' => (int) $request->query('edit', 0),
        ]);
    }

    /** @param callable(array<string, mixed>): string $fn */
    private function domainAct(Request $request, string $id, callable $fn): Response
    {
        $domain = $this->mail->domain((int) $id, $this->user($request));
        if ($domain === null) {
            return $this->redirectWithError('/mail', 'Mail domain not found.');
        }
        return $this->act($request, '/mail/domains/' . (int) $domain['id'], static fn (): string => $fn($domain));
    }

    public function publishDns(Request $request, string $id): Response
    {
        return $this->domainAct($request, $id, function (array $domain) use ($request): string {
            $n = $this->mail->publishDns($domain, (string) ($this->mail->status()['hostname'] ?? ''), $this->user($request));
            return $n > 0 ? $n . ' record(s) added to the DNS zone.' : 'The DNS zone already has these records.';
        });
    }

    public function saveMailbox(Request $request, string $id, ?string $mailbox = null): Response
    {
        return $this->domainAct($request, $id, function (array $domain) use ($request, $mailbox): string {
            $pw = $this->mail->saveMailbox($domain, $mailbox !== null ? (int) $mailbox : null, $request->all());
            $address = ($mailbox !== null ? '' : strtolower(trim($request->string('local')))) . '@' . $domain['domain'];
            if ($pw !== null) {
                $this->session->flash('secret', ['title' => 'Mailbox password', 'user' => $mailbox !== null ? 'the mailbox' : $address, 'password' => $pw]);
            }
            return $mailbox !== null ? 'Mailbox saved.' : 'Mailbox ' . $address . ' created.';
        });
    }

    public function updateMailbox(Request $request, string $id, string $mailbox): Response
    {
        return $this->saveMailbox($request, $id, $mailbox);
    }

    public function deleteMailbox(Request $request, string $id, string $mailbox): Response
    {
        return $this->domainAct($request, $id, function (array $domain) use ($request, $mailbox): string {
            $this->mail->deleteMailbox($domain, (int) $mailbox, $request->string('confirm'));
            return 'Mailbox deleted. Its mail was moved to the server\'s trash folder.';
        });
    }

    public function saveAlias(Request $request, string $id): Response
    {
        return $this->domainAct($request, $id, function (array $domain) use ($request): string {
            $this->mail->saveAlias($domain, $request->all());
            return 'Alias saved.';
        });
    }

    public function deleteAlias(Request $request, string $id, string $alias): Response
    {
        return $this->domainAct($request, $id, function (array $domain) use ($alias): string {
            $this->mail->deleteAlias($domain, (int) $alias);
            return 'Alias deleted.';
        });
    }

    public function delete(Request $request, string $id): Response
    {
        $domain = $this->mail->domain((int) $id, $this->user($request));
        if ($domain === null) {
            return $this->redirectWithError('/mail', 'Mail domain not found.');
        }
        try {
            $this->mail->removeDomain($domain, $request->string('confirm'));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/mail/domains/' . (int) $domain['id'], $e, []);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail/domains/' . (int) $domain['id'], $e);
        }
        return $this->redirectWithSuccess('/mail', 'Email is no longer hosted for ' . $domain['domain'] . '.');
    }

    public function site(Request $request, string $id): Response
    {
        $site = $this->sites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        $d = $this->mail->forWebsite((int) $site['id']);
        if ($d !== null && $this->mail->domain((int) $d['id'], $this->user($request)) !== null) {
            return Response::redirect('/mail/domains/' . (int) $d['id']);
        }
        return Response::redirect('/mail?website=' . (int) $site['id']);
    }
}
