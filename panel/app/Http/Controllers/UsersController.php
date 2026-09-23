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
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->render('users/index', [
            'users' => $this->users->all(),
            'roles' => $this->roles->all(),
            'currentUserId' => $this->userId($request),
        ]);
    }

    public function create(Request $request): Response
    {
        return $this->render('users/form', ['editUser' => null, 'roles' => $this->roles->all()]);
    }

    public function store(Request $request): Response
    {
        $input = $request->all();
        try {
            $id = $this->service->create($input, $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/users/new', $e, $input);
        }
        $user = $this->users->find($id);
        return $this->redirectWithSuccess('/users', 'User "' . ($user['username'] ?? '') . '" was created.');
    }

    public function edit(Request $request, string $id): Response
    {
        $user = $this->users->find((int) $id);
        if ($user === null) {
            return $this->redirectWithError('/users', 'The user no longer exists.');
        }
        return $this->render('users/form', ['editUser' => $user, 'roles' => $this->roles->all()]);
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
}
