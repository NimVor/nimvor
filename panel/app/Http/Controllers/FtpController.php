<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/**
 * FTP (pure-ftpd virtual accounts, TLS required by default) and SFTP
 * (the website's own system user, chrooted by OpenSSH) access to websites.
 * The agent keeps the accounts; the panel only asks for them per website.
 */
final class FtpController extends SiteController
{
    /** Accounts per website (a safety limit, not a plan limit). */
    public const MAX_ACCOUNTS = 25;

    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly \WhaCP\Services\SiteOverview $overview,
        private readonly \WhaCP\Services\PlanLimits $limits,
        private readonly \WhaCP\Services\FtpUsage $ftpUsage,
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
            $status = (array) $this->agent->call('ftp.status', ['user' => (string) $site['system_user']]);
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
        return strtolower(trim($name)) . '@' . strtolower($domain);
    }

    public function saveAccount(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'ftp', function (array $site) use ($request): string {
            $domain = (string) $site['domain'];
            $name = strtolower(trim($request->string('name')));
            $existing = $request->input('existing') === '1';
            if ($existing) {
                // Editing: the full login name comes back as is.
                $login = $name;
                if (!str_ends_with($login, '@' . strtolower($domain))) {
                    throw new UserFacingException('This account does not belong to the website.');
                }
            } else {
                if (!preg_match('/^[a-z0-9][a-z0-9._-]{0,30}$/', $name)) {
                    throw new ValidationException(['name' => 'Use up to 31 lower-case letters, digits and . _ -']);
                }
                $login = self::login($name, $domain);
                if (strlen($login) > 64) {
                    throw new ValidationException(['name' => 'The full login (' . $login . ') would be longer than 64 characters.']);
                }
            }
            $dir = trim($request->string('dir'), " /");
            if (preg_match('#(^|/)\.\.(/|$)#', $dir) || strlen($dir) > 300) {
                throw new ValidationException(['dir' => 'Enter a folder inside the website, e.g. public_html.']);
            }
            $password = $request->string('password');
            $generated = false;
            if ($request->input('generate') === '1') {
                $password = rtrim(strtr(base64_encode(random_bytes(15)), '+/', 'Aa'), '=');
                $generated = true;
            }
            if ($password !== '' && (strlen($password) < 10 || strlen($password) > 128)) {
                throw new ValidationException(['password' => 'Use 10–128 characters, or generate one.']);
            }
            if (!$existing) {
                if ($password === '') {
                    throw new ValidationException(['password' => 'Enter a password or generate one.']);
                }
                $count = count((array) (((array) $this->agent->call('ftp.status', ['user' => (string) $site['system_user']]))['accounts'] ?? []));
                if ($count >= self::MAX_ACCOUNTS) {
                    throw new UserFacingException('This website already has ' . self::MAX_ACCOUNTS . ' FTP accounts.');
                }
                $this->limits->assertCanCreate('ftp_accounts', $site['owner_id'] === null ? null : (int) $site['owner_id'], 1, fn (array $owners): int => $this->ftpUsage->count($owners));
            }
            $quota = max(0, (int) $request->input('quota_mb', 0));
            $params = ['name' => $login, 'user' => (string) $site['system_user'], 'dir' => $dir, 'quota_mb' => $quota];
            if ($password !== '') {
                $params['password'] = $password;
            }
            $this->agent->call('ftp.account_save', $params);
            $this->audit->record($existing ? 'ftp.account_update' : 'ftp.account_create', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['account' => $login, 'dir' => $dir, 'quota_mb' => $quota, 'password_changed' => $password !== '']);
            if ($generated) {
                $this->session->flash('secret', ['title' => 'FTP password for ' . $login, 'user' => $login, 'password' => $password]);
            }
            return $existing ? 'FTP account ' . $login . ' saved.' : 'FTP account ' . $login . ' created.';
        });
    }

    public function deleteAccount(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'ftp', function (array $site) use ($request): string {
            $login = strtolower(trim($request->string('name')));
            if (!str_ends_with($login, '@' . strtolower((string) $site['domain']))) {
                throw new UserFacingException('This account does not belong to the website.');
            }
            $this->agent->call('ftp.account_delete', ['name' => $login, 'user' => (string) $site['system_user']]);
            $this->audit->record('ftp.account_delete', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['account' => $login]);
            return 'FTP account ' . $login . ' deleted. Its files are kept.';
        });
    }

    public function sftp(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'ftp', function (array $site) use ($request): string {
            $enabled = $request->input('enabled') === '1';
            $keys = array_values(array_filter(array_map('trim', preg_split('/\R/', $request->string('keys')) ?: []), static fn (string $k): bool => $k !== '' && !str_starts_with($k, '#')));
            $passwordLogin = $request->input('password_login') === '1';
            $password = $request->string('sftp_password');
            if ($password !== '' && (strlen($password) < 10 || strlen($password) > 128)) {
                throw new ValidationException(['sftp_password' => 'Use 10–128 characters.']);
            }
            $params = ['user' => (string) $site['system_user'], 'enabled' => $enabled, 'keys' => $keys, 'password_login' => $passwordLogin];
            if ($password !== '') {
                $params['password'] = $password;
            }
            $this->agent->call('sftp.configure', $params, 60);
            $this->audit->record('sftp.configure', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['enabled' => $enabled, 'keys' => count($keys), 'password_login' => $passwordLogin, 'password_changed' => $password !== '']);
            return $enabled ? 'SFTP access saved.' : 'SFTP access turned off.';
        });
    }
}
