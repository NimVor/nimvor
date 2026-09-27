<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\AccountOptions;
use WhaCP\Services\AccountService;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\DnsService;
use WhaCP\Services\PlanService;
use WhaCP\Services\SettingsService;
use WhaCP\Services\UserService;
use WhaCP\Services\WebsiteService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

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
        private readonly \WhaCP\Services\MailService $mail,
    ) {
        parent::__construct($view, $session);
    }

    public function create(Request $request): Response
    {
        $ip = trim($this->settings->string('dns_default_ipv4'));
        return $this->render('accounts/create', [
            'plans' => $this->plans->visible($this->user($request)),
            'phpVersions' => $this->sites->phpVersions(),
            'canReseller' => Scope::isAll($this->user($request)),
            'spf' => DnsService::spf(filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false ? $ip : null),
            'nameservers' => $this->dns->nameservers(),
            'defaults' => AccountOptions::DEFAULTS,
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
        } catch (\WhaCP\Agent\AgentException $e) {
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
}
