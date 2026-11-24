<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\AuditLogger;
use NimVor\Services\FtpAccountService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * FTP (pure-ftpd virtual accounts, TLS required by default) and SFTP
 * (the website's own system user, chrooted by OpenSSH) access to websites.
 * The agent keeps the accounts; the panel only asks for them per website.
 */
final class FtpController extends SiteController
{
    public const MAX_ACCOUNTS = FtpAccountService::MAX_ACCOUNTS;

    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly \NimVor\Services\SiteOverview $overview,
        private readonly FtpAccountService $accounts,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    // ---- server ---------------------------------------------------------

    public function index(Request $request): Response
    {
        $status = null;
        $error = null;
        try {
            $status = (array) $this->agent->call('ftp.status');
        } catch (AgentException $e) {
            $error = $e;
        }
        $sites = [];
        foreach ($this->sites->all($this->server->id(), $this->user($request)) as $s) {
            $sites[(string) $s['system_user']] = $s;
        }
        return $this->render('ftp/index', ['status' => $status, 'agentError' => $error, 'sitesByUser' => $sites, 'serverIp' => $error === null ? $this->overview->serverIp() : null]);
    }

    /** @param callable(): (string|array{0: string, 1: string}) $fn */
    private function serverAct(Request $request, callable $fn): Response
    {
        try {
            $res = $fn();
        } catch (ValidationException $e) {
            return $this->backWithErrors('/ftp', $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/ftp', $e);
        }
        if (is_array($res)) {
            $this->flashJob($res[1]);
            $res = $res[0];
        }
        return $this->redirectWithSuccess('/ftp', $res);
    }

    public function install(Request $request): Response
    {
        return $this->serverAct($request, function () use ($request): array {
            $res = (array) $this->agent->call('packages.install', ['name' => 'pure-ftpd']);
            $job = (string) $res['job_id'];
            $this->jobs->track($job, 'Install pure-ftpd', 'server', 'ftp', $this->userId($request));
            $this->audit->record('packages.install', 'server', 'ftp', AuditLogger::SUCCESS, ['name' => 'pure-ftpd']);
            return ['Installing pure-ftpd. Then set up the FTP server below.', $job];
        });
    }

    public function configure(Request $request): Response
    {
        return $this->serverAct($request, function () use ($request): string {
            $errors = [];
            $min = filter_var($request->input('passive_min'), FILTER_VALIDATE_INT);
            $max = filter_var($request->input('passive_max'), FILTER_VALIDATE_INT);
            if ($min === false || $max === false || $min < 1024 || $max > 65535 || $min > $max || $max - $min > 5000) {
                $errors['passive_min'] = 'Enter a range within 1024–65535, at most 5000 ports wide.';
            }
            $ip = trim($request->string('passive_ip'));
            if ($ip !== '' && filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false) {
                $errors['passive_ip'] = 'Enter an IPv4 address or leave empty.';
            }
            $clients = filter_var($request->input('max_clients'), FILTER_VALIDATE_INT, ['options' => ['min_range' => 1, 'max_range' => 1000]]);
            $perIp = filter_var($request->input('max_per_ip'), FILTER_VALIDATE_INT, ['options' => ['min_range' => 1, 'max_range' => 1000]]);
            if ($clients === false || $perIp === false || $perIp > $clients) {
                $errors['max_clients'] = 'Enter 1–1000 connections, and no more per address than in total.';
            }
            $tls = $request->string('tls') === 'optional' ? 'optional' : 'required';
            if ($errors !== []) {
                throw new ValidationException($errors);
            }
            $params = ['passive_min' => $min, 'passive_max' => $max, 'passive_ip' => $ip, 'max_clients' => $clients, 'max_per_ip' => $perIp, 'tls' => $tls, 'confirm_replace_service' => $request->input('confirm_replace') === '1'];
            $res = (array) $this->agent->call('ftp.configure', $params, 120);
            $this->audit->record('ftp.configure', 'server', 'ftp', AuditLogger::SUCCESS, $params + ['stopped' => $res['stopped'] ?? []]);
            return 'The FTP server is running. ' . implode(' ', array_map('strval', (array) ($res['notes'] ?? [])));
        });
    }

    public function disable(Request $request): Response
    {
        return $this->serverAct($request, function (): string {
            $this->agent->call('ftp.disable');
            $this->audit->record('ftp.disable', 'server', 'ftp', AuditLogger::SUCCESS);
            return 'The FTP server was stopped. Accounts are kept.';
        });
    }

    // ---- website tab ----------------------------------------------------

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $status = null;
        $error = null;
        try {
            $status = $this->accounts->status($site);
        } catch (AgentException $e) {
            $error = $e;
        }
        $sftp = null;
        foreach ((array) ($status['sftp'] ?? []) as $s) {
            $sftp = (array) $s;
        }
        return $this->render('ftp/site', [
            'site' => $site, 'tab' => 'ftp', 'status' => $status, 'agentError' => $error, 'sftp' => $sftp,
            'secret' => $this->session->pullFlash('secret'), 'host' => $this->hostName($request),
        ]);
    }

    private function hostName(Request $request): string
    {
        $h = (string) parse_url('http://' . $request->header('host'), PHP_URL_HOST);
        return $h !== '' ? $h : 'this server';
    }

    /** The login name of an account of this website: <name>@<domain>. */
    public static function login(string $name, string $domain): string
    {
        return FtpAccountService::login($name, $domain);
    }

    public function saveAccount(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'ftp', function (array $site) use ($request): string {
            $existing = $request->input('existing') === '1';
            $res = $this->accounts->save($site, [
                'name' => $request->string('name'), 'dir' => $request->string('dir'), 'password' => $request->string('password'),
                'generate' => $request->input('generate') === '1', 'quota_mb' => $request->input('quota_mb', 0),
            ], $existing);
            if ($res['generated']) {
                $this->session->flash('secret', ['title' => 'FTP password for ' . $res['login'], 'user' => $res['login'], 'password' => $res['password']]);
            }
            return $existing ? 'FTP account ' . $res['login'] . ' saved.' : 'FTP account ' . $res['login'] . ' created.';
        });
    }

    public function deleteAccount(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'ftp', function (array $site) use ($request): string {
            $login = $this->accounts->delete($site, $request->string('name'));
            return 'FTP account ' . $login . ' deleted. Its files are kept.';
        });
    }

    public function sftp(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'ftp', function (array $site) use ($request): string {
            $enabled = $request->input('enabled') === '1';
            $this->accounts->sftp($site, $enabled, preg_split('/\R/', $request->string('keys')) ?: [], $request->input('password_login') === '1', $request->string('sftp_password'));
            return $enabled ? 'SFTP access saved.' : 'SFTP access turned off.';
        });
    }
}
