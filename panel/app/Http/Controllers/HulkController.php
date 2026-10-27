<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Security\Countries;
use WhaCP\Services\GeoIpService;
use WhaCP\Services\HulkService;
use WhaCP\Services\SecurityService;
use WhaCP\Services\SettingsService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/** Security → cPHulk Brute Force Protection (administrators). */
final class HulkController extends Controller
{
    private const KEYS = ['hulk_enabled', 'hulk_user_enabled', 'hulk_user_period', 'hulk_user_max', 'hulk_user_scope', 'hulk_user_lock_root',
        'hulk_ip_enabled', 'hulk_ip_period', 'hulk_ip_max', 'hulk_ip_command', 'hulk_ip_firewall',
        'hulk_oneday_max', 'hulk_oneday_command', 'hulk_oneday_firewall', 'hulk_retention',
        'hulk_notify_root', 'hulk_notify_root_netblock', 'hulk_notify_brute'];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly HulkService $hulk,
        private readonly GeoIpService $geo,
        private readonly SettingsService $settings,
        private readonly SecurityService $security,
        private readonly \WhaCP\Agent\AgentClient $agent,
        private readonly \WhaCP\Core\Config $config,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        if (($denied = $this->deny($request)) !== null) {
            return $denied;
        }
        $status = null;
        $error = null;
        try {
            $status = (array) $this->agent->call('hulk.status');
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('security/hulk', [
            'tab' => 'hulk', 'pending' => $this->security->pending(), 'agentError' => $error, 'status' => $status,
            'values' => $this->settings->all(), 'whitelist' => implode("\n", $this->hulk->whitelist()),
            'countries' => $this->hulk->countries(), 'geoLoaded' => $this->geo->loaded(),
            'report' => $this->hulk->report(), 'blocks' => $this->hulk->blocks(), 'variables' => HulkService::VARIABLES,
        ]);
    }

    public function save(Request $request): Response
    {
        if (($denied = $this->deny($request, true)) !== null) {
            return $denied;
        }
        $input = array_intersect_key($request->all(), array_flip(self::KEYS));
        if (!in_array($input['hulk_user_scope'] ?? 'all', ['local', 'all'], true)) {
            return $this->redirectWithError('/security/hulk', 'Choose a protection type.');
        }
        $entries = array_values(array_filter(preg_split('/[\s,]+/', (string) $request->input('hulk_whitelist', '')) ?: [], static fn (string $s): bool => $s !== ''));
        foreach ($entries as $w) {
            if (!HulkService::validEntry($w)) {
                return $this->redirectWithError('/security/hulk', sprintf('"%s" is not an IP address or network.', mb_substr($w, 0, 60)));
            }
        }
        if (count($entries) > 200) {
            return $this->redirectWithError('/security/hulk', 'At most 200 whitelisted addresses.');
        }
        // Turning protection on with an empty whitelist: this administrator's
        // address is added, so they cannot lock themselves out.
        $note = '';
        if ($entries === [] && ($input['hulk_enabled'] ?? '') === '1' && !$this->settings->bool('hulk_enabled')) {
            /** @var list<string> $proxies */
            $proxies = (array) $this->config->get('security.trusted_proxies', []);
            $entries[] = $request->ip($proxies);
            $note = ' Your address (' . $entries[0] . ') was added to the whitelist.';
        }
        $input['hulk_whitelist'] = implode(', ', $entries);
        foreach (['hulk_ip_command', 'hulk_oneday_command'] as $k) {
            $input[$k] = trim((string) ($input[$k] ?? ''));
        }
        try {
            $this->settings->update($input, (int) $this->user($request)['id']);
        } catch (ValidationException $e) {
            return $this->redirectWithError('/security/hulk', implode(' ', $e->errors()));
        }
        try {
            $this->hulk->sync();
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/security/hulk', $e);
        }
        return $this->redirectWithSuccess('/security/hulk', 'cPHulk settings saved.' . $note);
    }

    public function countries(Request $request): Response
    {
        if (($denied = $this->deny($request, true)) !== null) {
            return $denied;
        }
        $marks = [];
        $bulk = (string) $request->input('bulk', '');
        if ($bulk !== '') {
            if (!in_array($bulk, ['whitelist', 'blacklist', 'none'], true)) {
                return $this->redirectWithError('/security/hulk#countries', 'Choose how to mark the countries.');
            }
            foreach ((array) $request->input('selected', []) as $code) {
                $marks[(string) $code] = $bulk;
            }
        } else {
            foreach ((array) $request->input('country', []) as $code => $status) {
                $marks[(string) $code] = (string) $status;
            }
        }
        $scope = (string) $request->input('scope', '');
        if ($scope !== '') {
            if (!in_array($scope, ['login', 'all'], true)) {
                return $this->redirectWithError('/security/hulk#countries', 'Choose where blacklisted countries are blocked.');
            }
            $this->settings->update(['hulk_countries_all' => $scope === 'all' ? '1' : '0'], (int) $this->user($request)['id']);
        }
        $n = $this->hulk->setCountries($marks);
        try {
            $this->hulk->sync();
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/security/hulk#countries', $e);
        }
        return $this->redirectWithSuccess('/security/hulk#countries', sprintf('%d countr%s updated.', $n, $n === 1 ? 'y' : 'ies'));
    }

    public function geoUpload(Request $request): Response
    {
        if (($denied = $this->deny($request, true)) !== null) {
            return $denied;
        }
        $up = $_FILES['database'] ?? null;
        if (!is_array($up) || ($up['error'] ?? UPLOAD_ERR_NO_FILE) !== UPLOAD_ERR_OK || !is_uploaded_file((string) $up['tmp_name'])) {
            return $this->redirectWithError('/security/hulk#countries', 'Choose the DB-IP "IP to Country Lite" CSV file (.csv or .csv.gz).');
        }
        try {
            $n = $this->geo->import((string) $up['tmp_name']);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/security/hulk#countries', $e);
        }
        return $this->redirectWithSuccess('/security/hulk#countries', sprintf('Country database loaded (%d address ranges).', $n));
    }

    public function unblock(Request $request): Response
    {
        if (($denied = $this->deny($request, true)) !== null) {
            return $denied;
        }
        $this->hulk->removeBlock((int) $request->input('id'));
        return $this->redirectWithSuccess('/security/hulk#report', 'The block was removed.');
    }

    public function clear(Request $request): Response
    {
        if (($denied = $this->deny($request, true)) !== null) {
            return $denied;
        }
        $this->hulk->clear();
        return $this->redirectWithSuccess('/security/hulk#report', 'All blocks were removed and the report was cleared.');
    }

    private function deny(Request $request, bool $change = false): ?Response
    {
        $user = $this->user($request);
        if (!Scope::isAll($user) || ($change && ($user['role_name'] ?? '') !== 'admin')) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Only administrators manage brute force protection.', 'status' => 403], 403);
        }
        return null;
    }
}
