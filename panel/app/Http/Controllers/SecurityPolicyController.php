<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Core\Config;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\RoleRepository;
use WhaCP\Security\LoginIpVerifier;
use WhaCP\Security\PasswordPolicy;
use WhaCP\Services\RoleService;
use WhaCP\Services\SecurityService;
use WhaCP\Services\SettingsService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/** Security → Configure Security Policies and Password Strength Configuration (administrators). */
final class SecurityPolicyController extends Controller
{
    private const POLICY_KEYS = ['policy_verified_ips', 'policy_password_strength', 'policy_password_age_days'];
    private const STRENGTH_KEYS = ['password_min_strength', 'password_min_length', 'password_min_special', 'password_min_numeric', 'password_min_alpha', 'password_min_upper', 'password_min_lower'];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SettingsService $settings,
        private readonly RoleRepository $roles,
        private readonly RoleService $roleService,
        private readonly LoginIpVerifier $ips,
        private readonly PasswordPolicy $policy,
        private readonly SecurityService $security,
        private readonly Config $config,
    ) {
        parent::__construct($view, $session);
    }

    public function policies(Request $request): Response
    {
        if (($denied = $this->deny($request)) !== null) {
            return $denied;
        }
        return $this->render('security/policies', [
            'tab' => 'policies', 'pending' => $this->security->pending(), 'agentError' => null,
            'values' => $this->settings->all(), 'roles' => $this->roles->all(),
            'myIps' => $this->ips->list((int) $this->user($request)['id']),
            'rules' => $this->policy->describe(),
        ]);
    }

    public function savePolicies(Request $request): Response
    {
        if (($denied = $this->deny($request, true)) !== null) {
            return $denied;
        }
        $wasOn = $this->settings->bool('policy_verified_ips');
        try {
            $this->settings->update(array_intersect_key($request->all(), array_flip(self::POLICY_KEYS)), (int) $this->user($request)['id']);
            $required = array_map('intval', (array) $request->input('two_factor_roles', []));
            foreach ($this->roles->all() as $role) {
                $want = in_array((int) $role['id'], $required, true);
                if ($want !== !empty($role['require_two_factor'])) {
                    $this->roleService->setRequireTwoFactor((int) $role['id'], $want);
                }
            }
        } catch (ValidationException $e) {
            return $this->redirectWithError('/security/policies', implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/security/policies', $e);
        }
        $note = '';
        if (!$wasOn && $this->settings->bool('policy_verified_ips')) {
            // Nobody is locked out by turning it on: the address each user
            // last signed in from, and this one, count as verified.
            /** @var list<string> $proxies */
            $proxies = (array) $this->config->get('security.trusted_proxies', []);
            $this->ips->remember((int) $this->user($request)['id'], $request->ip($proxies));
            $n = $this->ips->seedFromLastLogins();
            $note = sprintf(' Each user\'s last sign-in address (%d) counts as verified.', $n);
        }
        return $this->redirectWithSuccess('/security/policies', 'Security policies saved.' . $note);
    }

    public function forgetIp(Request $request): Response
    {
        $this->ips->forget((int) $this->user($request)['id'], (int) $request->input('id'));
        return $this->redirectWithSuccess('/security/policies#verified-ips', 'The address is no longer verified.');
    }

    public function strength(Request $request): Response
    {
        if (($denied = $this->deny($request)) !== null) {
            return $denied;
        }
        return $this->render('security/password_strength', [
            'tab' => 'password_strength', 'pending' => $this->security->pending(), 'agentError' => null,
            'values' => $this->settings->all(), 'definitions' => SettingsService::definitions(),
            'rules' => $this->policy->describe(),
            'examples' => array_map(static fn (string $p): array => [$p, PasswordPolicy::strength($p)], ['password1234', 'sunflower2024', 'Sunflower!2024', 'correct-horse-battery-staple']),
        ]);
    }

    public function saveStrength(Request $request): Response
    {
        if (($denied = $this->deny($request, true)) !== null) {
            return $denied;
        }
        try {
            $this->settings->update(array_intersect_key($request->all(), array_flip(self::STRENGTH_KEYS)), (int) $this->user($request)['id']);
        } catch (ValidationException $e) {
            return $this->redirectWithError('/security/password-strength', implode(' ', $e->errors()));
        }
        return $this->redirectWithSuccess('/security/password-strength', 'Password strength requirements saved. They apply to passwords set from now on.');
    }

    private function deny(Request $request, bool $change = false): ?Response
    {
        $user = $this->user($request);
        if (!Scope::isAll($user) || ($change && ($user['role_name'] ?? '') !== 'admin')) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Only administrators manage security policies.', 'status' => 403], 403);
        }
        return null;
    }
}
