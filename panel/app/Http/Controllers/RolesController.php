<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\RoleRepository;
use WhaCP\Services\RoleService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class RolesController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly RoleRepository $roles,
        private readonly RoleService $service,
        private readonly \WhaCP\Http\Router $router,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->render('roles/index', ['roles' => $this->roles->all()]);
    }

    public function create(Request $request): Response
    {
        return $this->render('roles/form', $this->formData(null, [], $request));
    }

    public function store(Request $request): Response
    {
        $input = $request->all();
        try {
            $this->service->create($input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/roles/new', $e, $input);
        }
        return $this->redirectWithSuccess('/roles', 'Role created.');
    }

    public function edit(Request $request, string $id): Response
    {
        $role = $this->roles->find((int) $id);
        if ($role === null) {
            return $this->redirectWithError('/roles', 'The role no longer exists.');
        }
        return $this->render('roles/form', $this->formData($role, $this->roles->permissionsFor((int) $id), $request));
    }

    public function update(Request $request, string $id): Response
    {
        $input = $request->all();
        try {
            $this->service->update((int) $id, $input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/roles/' . (int) $id . '/edit', $e, $input);
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/roles', $e->getMessage());
        }
        return $this->redirectWithSuccess('/roles', 'Role updated.');
    }

    public function reset(Request $request, string $id): Response
    {
        try {
            $this->service->resetDefaults((int) $id);
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/roles', $e->getMessage());
        }
        return $this->redirectWithSuccess('/roles/' . (int) $id . '/edit', 'The role has WhaCP\'s default permissions again.');
    }

    /**
     * @param array<string, mixed>|null $role
     * @param list<string> $granted
     * @return array<string, mixed>
     */
    private function formData(?array $role, array $granted, Request $request): array
    {
        return [
            'role' => $role,
            'granted' => $granted,
            'permissions' => $this->roles->allPermissions(),
            'coverage' => \WhaCP\View\Navigation::coverage($this->router),
            'own' => $this->roles->permissionsFor((int) ($this->user($request)['role_id'] ?? 0)),
            'ownScopeAllowed' => \WhaCP\Security\Permissions::ownScopeAllowed(),
            'missingDefaults' => $role !== null ? $this->service->missingDefaults($role) : [],
        ];
    }

    public function destroy(Request $request, string $id): Response
    {
        try {
            $this->service->delete((int) $id);
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/roles', trim($e->getMessage() . ' ' . $e->remedy()));
        }
        return $this->redirectWithSuccess('/roles', 'Role deleted.');
    }

    public function twoFactorPolicy(Request $request, string $id): Response
    {
        try {
            $this->service->setRequireTwoFactor((int) $id, $request->input('require_two_factor') === '1');
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/roles', $e->getMessage());
        }
        return $this->redirectWithSuccess('/roles/' . (int) $id . '/edit', 'Sign-in policy saved. Members without two-factor sign-in must set it up at their next page load.');
    }
}
