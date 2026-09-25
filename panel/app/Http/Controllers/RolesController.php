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
    ) {
        parent::__construct($view, $session);
    }

    public function create(Request $request): Response
    {
        return $this->render('roles/form', ['role' => null, 'granted' => [], 'permissions' => $this->roles->allPermissions()]);
    }

    public function store(Request $request): Response
    {
        $input = $request->all();
        try {
            $this->service->create($input);
        } catch (ValidationException $e) {
            return $this->backWithErrors('/roles/new', $e, $input);
        }
        return $this->redirectWithSuccess('/users', 'Role created.');
    }

    public function edit(Request $request, string $id): Response
    {
        $role = $this->roles->find((int) $id);
        if ($role === null) {
            return $this->redirectWithError('/users', 'The role no longer exists.');
        }
        return $this->render('roles/form', [
            'role' => $role,
            'granted' => $this->roles->permissionsFor((int) $id),
            'permissions' => $this->roles->allPermissions(),
        ]);
    }

    public function update(Request $request, string $id): Response
    {
        $input = $request->all();
        try {
            $this->service->update((int) $id, $input);
        } catch (ValidationException $e) {
            return $this->backWithErrors('/roles/' . (int) $id . '/edit', $e, $input);
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/users', $e->getMessage());
        }
        return $this->redirectWithSuccess('/users', 'Role updated.');
    }

    public function destroy(Request $request, string $id): Response
    {
        try {
            $this->service->delete((int) $id);
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/users', trim($e->getMessage() . ' ' . $e->remedy()));
        }
        return $this->redirectWithSuccess('/users', 'Role deleted.');
    }

    public function twoFactorPolicy(Request $request, string $id): Response
    {
        try {
            $this->service->setRequireTwoFactor((int) $id, $request->input('require_two_factor') === '1');
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/users', $e->getMessage());
        }
        return $this->redirectWithSuccess('/roles/' . (int) $id . '/edit', 'Sign-in policy saved. Members without two-factor sign-in must set it up at their next page load.');
    }
}
