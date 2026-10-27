<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\RoleRepository;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\UserService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class UsersController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly UserRepository $users,
        private readonly RoleRepository $roles,
        private readonly UserService $service,
        private readonly \WhaCP\Services\PlanService $plans,
        private readonly \WhaCP\Services\PlanLimits $limits,
        private readonly \WhaCP\Security\TwoFactor $twoFactor,
        private readonly \WhaCP\Security\WebAuthn $webAuthn,
        private readonly \WhaCP\Services\AuditLogger $audit,
        private readonly \WhaCP\Services\AccountOptions $hostingOptions,
        private readonly \WhaCP\Services\AccountAdminService $accountAdmin,
    ) {
        parent::__construct($view, $session);
    }

    /** @return list<array<string, mixed>> roles the actor may hand out */
    private function assignableRoles(Request $request): array
    {
        $mine = $this->roles->permissionsFor((int) ($this->user($request)['role_id'] ?? 0));
        $own = \WhaCP\Hosting\Scope::isAll($this->user($request));
        return array_values(array_filter($this->roles->all(), function (array $r) use ($mine, $own): bool {
            $perms = $this->roles->permissionsFor((int) $r['id']);
            return array_diff($perms, $mine) === [] && ($own || !in_array(\WhaCP\Security\Permissions::USERS_MANAGE, $perms, true));
        }));
    }

    public function index(Request $request): Response
    {
        return $this->render('users/index', [
            'users' => $this->users->manageableBy($this->user($request)),
            'roles' => $this->roles->all(),
            'currentUserId' => $this->userId($request),
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->render('users/form', ['editUser' => null, 'roles' => $this->assignableRoles($request), 'plans' => $this->plans->visible($this->user($request))]);
    }

    public function store(Request $request): Response
    {
        $input = $request->all();
        try {
            $id = $this->service->create($input, $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/users/new', $e, $input);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/users', $e);
        }
        $user = $this->users->find($id);
        return $this->redirectWithSuccess('/users', 'User "' . ($user['username'] ?? '') . '" was created.');
    }

    public function edit(Request $request, string $id): Response
    {
        $user = $this->users->find((int) $id);
        if ($user === null || !\WhaCP\Services\UserService::canManage($this->user($request), $user)) {
            return $this->redirectWithError('/users', 'The user no longer exists.');
        }
        return $this->render('users/form', ['editUser' => $user, 'roles' => $this->assignableRoles($request), 'plans' => $this->plans->visible($this->user($request)), 'usage' => $this->limits->usage((int) $user['id']), 'twoFactor' => $this->twoFactor->status((int) $user['id']), 'hostingOptions' => ($user['role_scope'] ?? 'all') === 'own' ? $this->hostingOptions->get((int) $user['id']) : null, 'limitRows' => ($user['role_scope'] ?? 'all') === 'own' && \WhaCP\Hosting\Scope::isAll($this->user($request)) ? $this->accountAdmin->limitsTable($user) : null]);
    }

    public function update(Request $request, string $id): Response
    {
        $input = $request->all();
        $input['is_active'] = $request->input('is_active') === '1';
        try {
            $this->service->update((int) $id, $input, $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/users/' . (int) $id . '/edit', $e, $input);
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/users', $e->getMessage());
        }
        return $this->redirectWithSuccess('/users', 'User updated.');
    }

    public function destroy(Request $request, string $id): Response
    {
        try {
            $this->service->delete((int) $id, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/users', trim($e->getMessage() . ' ' . $e->remedy()));
        }
        return $this->redirectWithSuccess('/users', 'User deleted.');
    }

    public function resetTwoFactor(Request $request, string $id): Response
    {
        $user = $this->users->find((int) $id);
        if ($user === null || !UserService::canManage($this->user($request), $user)) {
            return $this->redirectWithError('/users', 'The user no longer exists.');
        }
        $this->twoFactor->disable((int) $user['id']);
        $this->webAuthn->deleteAll((int) $user['id']);
        $this->audit->record('user.two_factor_reset', 'user', (string) $user['id'], \WhaCP\Services\AuditLogger::SUCCESS, ['username' => $user['username']]);
        return $this->redirectWithSuccess('/users/' . (int) $user['id'] . '/edit', 'Two-factor sign-in (app and security keys) is off for ' . $user['username'] . '. Ask them to turn it on again.');
    }
}
