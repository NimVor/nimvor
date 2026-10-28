<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Mail\MailConfig;
use NimVor\Security\Permissions as P;
use NimVor\Services\AuditLogger;
use NimVor\Services\HulkService;
use NimVor\Services\PlanService;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Basic WebHost Settings (the essentials of a new server: contact, panel
 * address, name servers, defaults for new accounts) and Tweak Settings
 * (every panel setting in tabs, with search, its default, whether it was
 * changed and a reset), in the manner of cPanel's WHM. Both edit the same
 * settings as the Settings page and the pages that own them.
 */
final class WebHostSettingsController extends Controller
{
    /** Tweak Settings tabs: category => [label, icon]. */
    public const TABS = [
        'general' => ['General', 'sliders'],
        'accounts' => ['Accounts and websites', 'person-gear'],
        'dns' => ['DNS', 'signpost'],
        'ssl' => ['SSL/TLS', 'shield-lock'],
        'monitoring' => ['Monitoring', 'activity'],
        'security' => ['Security', 'shield-check'],
        'policy' => ['Security policies', 'person-lock'],
        'hulk' => ['Brute force protection', 'shield-exclamation'],
    ];

    /**
     * Settings kept on their own page only: turning them on does more than
     * store a value (the administrator's address is whitelisted or marked
     * verified so nobody is locked out), or they are lists or state.
     */
    public const OWN_PAGE = [
        'hulk_enabled' => 'security.hulk', 'policy_verified_ips' => 'security.policies', 'hulk_whitelist' => 'security.hulk',
        'hulk_synced' => null, 'hulk_geo_updated' => null, 'hulk_geo_tried' => null,
    ];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SettingsService $settings,
        private readonly MailConfig $mail,
        private readonly PlanService $plans,
        private readonly WebsiteService $sites,
        private readonly AgentClient $agent,
        private readonly HulkService $hulk,
        private readonly AuditLogger $audit,
        private readonly \NimVor\Security\AuthService $auth,
    ) {
        parent::__construct($view, $session);
    }

    // ---- Basic WebHost Settings ------------------------------------------

    /** Name server fields on the Basic page. */
    private const NS_FIELDS = 4;

    public function basic(Request $request): Response
    {
        $hostname = '';
        $addresses = [];
        try {
            $info = (array) $this->agent->call('system.info');
            $sys = (array) ($info['system'] ?? []);
            $hostname = (string) ($sys['hostname'] ?? '');
            foreach ((array) ($sys['interfaces'] ?? []) as $if) {
                foreach ((array) ($if['addresses'] ?? []) as $a) {
                    $ip = explode('/', (string) $a)[0];
                    if (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4 | FILTER_FLAG_NO_RES_RANGE) !== false) {
                        $addresses[] = $ip;
                    }
                }
            }
        } catch (AgentException) {
            // The page works without them.
        }
        $ns = preg_split('/[\s,]+/', $this->settings->string('dns_nameservers'), -1, PREG_SPLIT_NO_EMPTY) ?: [];
        return $this->render('settings/basic', [
            'values' => $this->settings->all(), 'mail' => $this->mail->get(), 'hostname' => $hostname,
            'addresses' => array_values(array_unique($addresses)), 'ns' => array_pad(array_slice($ns, 0, self::NS_FIELDS), self::NS_FIELDS, ''),
            'plans' => $this->plans->visible($this->user($request)), 'phpVersions' => $this->sites->phpVersions(),
            'definitions' => SettingsService::definitions(), 'canEdit' => $this->view->can(P::SETTINGS_MANAGE),
        ]);
    }

    public function saveBasic(Request $request): Response
    {
        $tab = (string) $request->input('tab', 'contact');
        $back = '/settings/basic' . (preg_match('/^[a-z]{2,20}$/', $tab) ? '#' . $tab : '');
        $input = $request->all();
        try {
            switch ($tab) {
                case 'contact':
                    // The server contact receives alerts: the mail settings
                    // are kept as they are (an empty password keeps the stored one).
                    $mail = $this->mail->get();
                    $this->mail->save(['host' => $mail['host'], 'port' => (string) $mail['port'], 'encryption' => $mail['encryption'], 'username' => $mail['username'], 'password' => '', 'from' => $mail['from'], 'alert_to' => (string) ($input['alert_to'] ?? '')], $this->userId($request));
                    break;
                case 'basic':
                    $this->settings->update(array_intersect_key($input, array_flip(['panel_name', 'panel_url', 'dns_default_ipv4', 'dns_hostmaster'])), $this->userId($request));
                    break;
                case 'nameservers':
                    $list = [];
                    $errors = [];
                    for ($i = 1; $i <= self::NS_FIELDS; $i++) {
                        $ns = strtolower(rtrim(trim((string) ($input['ns' . $i] ?? '')), '.'));
                        if ($ns === '') {
                            continue;
                        }
                        if (!preg_match('/^(?=.{4,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/', $ns)) {
                            $errors['ns' . $i] = 'Enter a host name such as ns' . $i . '.example.com.';
                        }
                        $list[] = $ns;
                    }
                    if ($errors !== []) {
                        throw new ValidationException($errors);
                    }
                    $this->settings->update(['dns_nameservers' => implode(', ', array_values(array_unique($list)))], $this->userId($request));
                    break;
                case 'accounts':
                    $keys = array_keys(array_filter(SettingsService::definitions(), static fn (array $d): bool => ($d['category'] ?? '') === 'accounts'));
                    $this->settings->update(array_intersect_key($input, array_flip($keys)), $this->userId($request));
                    break;
                default:
                    return $this->redirectWithError('/settings/basic', 'Unknown section.');
            }
        } catch (ValidationException $e) {
            return $this->backWithErrors($back, $e, $input);
        }
        return $this->redirectWithSuccess($back, 'Saved.');
    }

    // ---- Tweak Settings ---------------------------------------------------

    /** Whether the current user may change a tab's settings. */
    private function mayEdit(Request $request, string $tab): bool
    {
        if (!$this->view->can(P::SETTINGS_MANAGE)) {
            return false;
        }
        // Security policies and brute force protection: administrators only,
        // as on their own pages.
        if ($tab === 'policy' || $tab === 'hulk') {
            $user = $this->user($request);
            return Scope::isAll($user) && ($user['role_name'] ?? '') === 'admin' && $this->view->can(P::SECURITY_MANAGE);
        }
        return true;
    }

    /** @return array<string, array<string, array<string, mixed>>> tab => key => definition */
    public static function tabs(): array
    {
        $out = array_fill_keys(array_keys(self::TABS), []);
        foreach (SettingsService::definitions() as $key => $def) {
            $cat = (string) ($def['category'] ?? '');
            if (isset($out[$cat]) && !(array_key_exists($key, self::OWN_PAGE) && self::OWN_PAGE[$key] === null)) {
                $out[$cat][$key] = $def;
            }
        }
        return $out;
    }

    public function tweak(Request $request): Response
    {
        $editable = [];
        foreach (array_keys(self::TABS) as $t) {
            $editable[$t] = $this->mayEdit($request, $t);
        }
        return $this->render('settings/tweak', [
            'tabs' => self::tabs(), 'labels' => self::TABS, 'values' => $this->settings->all(), 'editable' => $editable,
            'ownPage' => self::OWN_PAGE, 'plans' => $this->plans->visible($this->user($request)),
        ]);
    }

    /**
     * Saves one tab, or resets settings to their defaults (reset=key, or
     * reset=all for the tab).
     */
    public function saveTweak(Request $request): Response
    {
        $tab = (string) $request->input('tab', '');
        $tabs = self::tabs();
        if (!isset($tabs[$tab])) {
            return $this->redirectWithError('/settings/tweak', 'Unknown tab.');
        }
        $back = '/settings/tweak#' . $tab;
        if (!$this->mayEdit($request, $tab)) {
            return $this->redirectWithError($back, 'You may not change these settings' . ($tab === 'policy' || $tab === 'hulk' ? ': only administrators manage security policies and brute force protection.' : '.'));
        }
        // Sign-in policies need the password confirmed recently, as on
        // Security → Configure Security Policies (security.policies.save).
        if ($tab === 'policy' && !$this->auth->recentlyConfirmed()) {
            $this->session->flash('error', 'Confirm your password to continue, then submit again.');
            return Response::redirect('/confirm-password?return=' . rawurlencode('/settings/tweak#policy'));
        }
        $keys = array_keys(array_filter($tabs[$tab], static fn (string $k): bool => !array_key_exists($k, self::OWN_PAGE), ARRAY_FILTER_USE_KEY));
        $reset = (string) $request->input('reset', '');
        if ($reset !== '') {
            $which = $reset === 'all' ? $keys : array_intersect([$reset], $keys);
            if ($which === []) {
                return $this->redirectWithError($back, 'Unknown setting.');
            }
            $input = [];
            foreach ($which as $k) {
                $d = $tabs[$tab][$k]['default'];
                $input[$k] = is_bool($d) ? ($d ? '1' : '0') : (string) $d;
            }
        } else {
            $input = array_intersect_key($request->all(), array_flip($keys));
        }
        $before = $this->settings->all();
        try {
            $this->settings->update($input, $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors($back, $e, $request->all());
        }
        $changed = array_keys(array_filter($input, fn ($v, string $k): bool => $before[$k] !== $this->settings->get($k), ARRAY_FILTER_USE_BOTH));
        $note = '';
        if ($tab === 'hulk' && $changed !== []) {
            try {
                $this->hulk->sync();
            } catch (AgentException $e) {
                $note = ' The brute force protection rules were not applied yet (they are retried): ' . $e->getMessage();
            }
        }
        if ($reset !== '' && $changed !== []) {
            $this->audit->record('settings.reset', 'settings', 'panel', AuditLogger::SUCCESS, ['tab' => $tab, 'keys' => $changed]);
        }
        $msg = $changed === [] ? 'Nothing changed.' : ($reset !== '' ? 'Reset to the default: ' : 'Saved: ') . implode(', ', array_map(static fn (string $k): string => (string) $tabs[$tab][$k]['label'], $changed)) . '.';
        return $this->redirectWithSuccess($back, $msg . $note);
    }
}
