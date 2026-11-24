<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\UserRepository;
use NimVor\Services\AccountAdminService;
use NimVor\Services\AccountService;
use NimVor\Services\PlanService;
use NimVor\Services\ResourceLimitService;
use NimVor\Services\SuspensionService;
use NimVor\Services\TerminationService;
use NimVor\Services\UserService;
use NimVor\Support\UserFacingException;

/**
 * REST API v1: panel users, hosting accounts and plans. Every endpoint calls the same services
 * as the web interface, so the rules are the same: nobody can hand out a role with permissions
 * they lack, the last administrator stays, resellers only see their own customers.
 */
final class UsersApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly UserRepository $users,
        private readonly UserService $service,
        private readonly AccountService $accounts,
        private readonly AccountAdminService $accountAdmin,
        private readonly PlanService $plans,
        private readonly SuspensionService $suspension,
        private readonly TerminationService $termination,
        private readonly ResourceLimitService $limits,
    ) {
    }

    /**
     * @param array<string, mixed> $u
     * @return array<string, mixed>
     */
    private static function userOut(array $u): array
    {
        return [
            'id' => (int) $u['id'],
            'username' => $u['username'],
            'email' => $u['email'],
            'display_name' => $u['display_name'],
            'role' => ['id' => (int) $u['role_id'], 'name' => $u['role_name'], 'label' => $u['role_label'], 'scope' => $u['role_scope']],
            'is_active' => (bool) $u['is_active'],
            'suspended' => !empty($u['suspended']),
            'suspend_reason' => (string) ($u['suspend_reason'] ?? ''),
            'plan_id' => ($u['plan_id'] ?? null) === null ? null : (int) $u['plan_id'],
            'plan_name' => $u['plan_name'] ?? null,
            'parent_id' => ($u['parent_id'] ?? null) === null ? null : (int) $u['parent_id'],
            'last_login_at' => $u['last_login_at'] ?: null,
            'created_at' => $u['created_at'],
        ];
    }

    /**
     * @param array<string, mixed> $p
     * @return array<string, mixed>
     */
    private static function planOut(array $p): array
    {
        $limits = [];
        foreach (array_keys($p) as $k) {
            if (str_starts_with((string) $k, 'max_') || in_array($k, ['disk_quota_mb', 'bandwidth_mb'], true)) {
                $limits[$k] = (int) $p[$k];
            }
        }
        return [
            'id' => (int) $p['id'], 'name' => $p['name'], 'description' => (string) $p['description'],
            'owner_id' => $p['owner_id'] === null ? null : (int) $p['owner_id'], 'users' => (int) ($p['users'] ?? 0), 'limits' => $limits,
        ];
    }

    /** @return array<string, mixed>|null the user when the token may manage it */
    private function managed(Request $r, string $id): ?array
    {
        $u = $this->users->find((int) $id);
        return $u !== null && UserService::canManage($this->user($r), $u) ? $u + ['plan_name' => null] : null;
    }

    /** The row with its plan name, as the list shows it. */
    private function fresh(int $id): array
    {
        $row = (array) $this->users->find($id);
        $plan = ($row['plan_id'] ?? null) === null ? null : $this->plans->find(['id' => 0, 'role_scope' => 'all'], (int) $row['plan_id']);
        return $row + ['plan_name' => $plan['name'] ?? null];
    }

    public function listUsers(Request $r): Response
    {
        return self::ok(array_map([self::class, 'userOut'], $this->users->manageableBy($this->user($r))));
    }

    public function showUser(Request $r, string $id): Response
    {
        $u = $this->managed($r, $id);
        return $u === null ? self::notFound() : self::ok(self::userOut($this->fresh((int) $u['id'])));
    }

    public function createUser(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $in = self::body($r);
            $generated = null;
            if (filter_var($in['generate_password'] ?? false, FILTER_VALIDATE_BOOL)) {
                $in['password'] = $generated = AccountService::password();
            }
            $in['password_confirmation'] = (string) ($in['password'] ?? '');
            $in['is_active'] = filter_var($in['is_active'] ?? true, FILTER_VALIDATE_BOOL);
            $id = $this->service->create($in, (int) $this->user($r)['id']);
            return self::ok(['user' => self::userOut($this->fresh($id)), 'password' => $generated], 201);
        });
    }

    public function updateUser(Request $r, string $id): Response
    {
        $existing = $this->managed($r, $id);
        if ($existing === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $existing): Response {
            $in = self::body($r);
            // A PATCH changes only what it names.
            foreach (['email', 'display_name', 'role_id'] as $k) {
                $in[$k] ??= $existing[$k];
            }
            $in['is_active'] = filter_var($in['is_active'] ?? (bool) $existing['is_active'], FILTER_VALIDATE_BOOL);
            if (isset($in['password'])) {
                $in['password_confirmation'] = (string) $in['password'];
            }
            $this->service->update((int) $existing['id'], $in, (int) $this->user($r)['id']);
            return self::ok(self::userOut($this->fresh((int) $existing['id'])));
        });
    }

    public function deleteUser(Request $r, string $id): Response
    {
        $existing = $this->managed($r, $id);
        if ($existing === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $existing): Response {
            $this->service->delete((int) $existing['id'], (int) $this->user($r)['id']);
            return self::ok(['deleted' => (int) $existing['id']]);
        });
    }

    public function listPlans(Request $r): Response
    {
        return self::ok(array_map([self::class, 'planOut'], $this->plans->visible($this->user($r))));
    }

    public function createAccount(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $in = self::body($r);
            if (!filter_var($in['generate_password'] ?? false, FILTER_VALIDATE_BOOL)) {
                $in['password_confirmation'] = (string) ($in['password'] ?? '');
            }
            $in['generate'] = $in['generate_password'] ?? false;
            $res = $this->accounts->create($in, $this->user($r));
            return self::ok(['user' => self::userOut($this->fresh($res['id'])), 'website_id' => $res['website_id'], 'password' => $res['password']], 201);
        });
    }

    public function suspend(Request $r, string $id): Response
    {
        return $this->setSuspended($r, $id, true);
    }

    public function unsuspend(Request $r, string $id): Response
    {
        return $this->setSuspended($r, $id, false);
    }

    private function setSuspended(Request $r, string $id, bool $suspend): Response
    {
        if ($this->managed($r, $id) === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $id, $suspend): Response {
            $warnings = $suspend
                ? $this->suspension->suspend((int) $id, (string) (self::body($r)['reason'] ?? ''), $this->user($r))
                : $this->suspension->unsuspend((int) $id, $this->user($r));
            return self::ok(['user' => self::userOut($this->fresh((int) $id)), 'warnings' => $warnings]);
        });
    }

    public function changePlan(Request $r, string $id): Response
    {
        if ($this->managed($r, $id) === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $id): Response {
            $account = $this->accountAdmin->account((int) $id, $this->user($r), false);
            $plan = $this->accountAdmin->changePlan($account, (int) (self::body($r)['plan_id'] ?? 0), $this->user($r));
            $warnings = [];
            try {
                $this->limits->reconcile();
            } catch (\NimVor\Agent\AgentException $e) {
                $warnings[] = 'The plan was changed, but its resource limits were not applied yet: ' . $e->getMessage();
            }
            return self::ok(['user' => self::userOut($this->fresh((int) $id)), 'plan' => self::planOut($plan), 'warnings' => $warnings]);
        });
    }

    public function terminate(Request $r, string $id): Response
    {
        if ($this->managed($r, $id) === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $id): Response {
            $confirm = trim((string) (self::body($r)['confirm'] ?? ''));
            $results = $this->termination->terminate([(int) $id], $confirm, 1, $this->user($r));
            $res = $results[0] ?? ['ok' => false, 'message' => 'Nothing was terminated.'];
            if (empty($res['ok'])) {
                throw new UserFacingException((string) $res['message'], status: 409);
            }
            return self::ok(['terminated' => (int) $id, 'message' => (string) $res['message']]);
        });
    }
}
