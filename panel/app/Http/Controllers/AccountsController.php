<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\UserRepository;
use NimVor\Services\AccountOptions;
use NimVor\Services\AccountService;
use NimVor\Services\AuditLogger;
use NimVor\Services\DnsService;
use NimVor\Services\PlanService;
use NimVor\Services\SettingsService;
use NimVor\Services\UserService;
use NimVor\Services\WebsiteService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/** Create a New Account (hosting account in one step) and its options. */
final class AccountsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AccountService $accounts,
        private readonly AccountOptions $options,
        private readonly PlanService $plans,
        private readonly WebsiteService $sites,
        private readonly SettingsService $settings,
        private readonly DnsService $dns,
        private readonly UserRepository $users,
        private readonly AuditLogger $audit,
        private readonly \NimVor\Services\MailService $mail,
        private readonly \NimVor\Services\SuspensionService $suspension,
    ) {
        parent::__construct($view, $session);
    }

    public function create(Request $request): Response
    {
        $ip = trim($this->settings->string('dns_default_ipv4'));
        $php = $this->sites->phpVersions();
        $defaults = AccountOptions::DEFAULTS;
        foreach (['dns_dkim', 'dns_spf', 'dns_dmarc', 'spam_filter', 'spam_box'] as $k) {
            $defaults[$k] = $this->settings->bool('account_' . $k);
        }
        $defaults['mail_routing'] = $this->settings->string('account_mail_routing');
        $defaults['plan_id'] = $this->settings->int('default_plan_id') > 0 ? (string) $this->settings->int('default_plan_id') : '';
        $defaults['php_version'] = $this->sites->defaultPhpVersion($php);
        return $this->render('accounts/create', [
            'plans' => $this->plans->visible($this->user($request)),
            'phpVersions' => $php,
            'canReseller' => Scope::isAll($this->user($request)),
            'spf' => DnsService::spf(filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false ? $ip : null),
            'nameservers' => $this->dns->nameservers(),
            'defaults' => $defaults,
        ]);
    }

    public function store(Request $request): Response
    {
        $input = $request->all();
        try {
            $res = $this->accounts->create($input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/accounts/new', $e, array_diff_key($input, ['password' => 1, 'password_confirmation' => 1]));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/accounts/new', $e);
        } catch (\NimVor\Agent\AgentException $e) {
            return $this->redirectWithError('/accounts/new', 'The account was not created: ' . $e->getMessage());
        }
        $name = (string) ($this->users->find($res['id'])['username'] ?? '');
        return $this->redirectWithSuccess('/users/' . $res['id'] . '/edit', 'Account ' . $name . ' was created' . ($res['website_id'] !== null ? ' with its website' : '') . '.');
    }

    /** Hosting options of an account (users → edit). */
    public function saveOptions(Request $request, string $id): Response
    {
        $target = $this->users->find((int) $id);
        if ($target === null || !UserService::canManage($this->user($request), $target) || ($target['role_scope'] ?? 'all') !== 'own') {
            return $this->redirectWithError('/users', 'The user no longer exists.');
        }
        $opts = $this->accounts->optionsFrom($request->all());
        $this->options->set((int) $target['id'], $opts);
        $synced = $this->mail->syncSpam();
        $this->audit->record('account.options', 'user', (string) $target['id'], AuditLogger::SUCCESS, ['username' => $target['username']] + $opts);
        if (!$synced) {
            return $this->redirectWithError('/users/' . (int) $target['id'] . '/edit', 'Hosting options saved, but the spam filter settings could not be applied to the mail server. Save the spam filter again under Email.');
        }
        return $this->redirectWithSuccess('/users/' . (int) $target['id'] . '/edit', 'Hosting options saved. DNS options apply to new websites; spam options to the account\'s email now.');
    }

    public function suspend(Request $request, string $id): Response
    {
        $to = '/users/' . (int) $id . '/edit';
        try {
            $warnings = $this->suspension->suspend((int) $id, $request->string('reason'), $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        if ($warnings !== []) {
            return $this->redirectWithError($to, 'The account is suspended (it cannot sign in), but some services could not be turned off: ' . implode('; ', $warnings) . '. Use "Suspend" again to retry.');
        }
        return $this->redirectWithSuccess($to, 'The account is suspended: its websites, email logins, databases, FTP/SFTP, cron jobs and workers are off. Nothing was deleted.');
    }

    public function unsuspend(Request $request, string $id): Response
    {
        $to = '/users/' . (int) $id . '/edit';
        try {
            $warnings = $this->suspension->unsuspend((int) $id, $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        if ($warnings !== []) {
            return $this->redirectWithError($to, 'The account is resumed, but some services could not be turned back on: ' . implode('; ', $warnings) . '.');
        }
        return $this->redirectWithSuccess($to, 'The account is resumed.');
    }
}
