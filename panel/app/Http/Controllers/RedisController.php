<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\RedisService;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class RedisController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly RedisService $redis,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $info = null;
        $error = null;
        try {
            $info = $this->redis->info();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('redis/index', [
            'info' => $info,
            'agentError' => $error,
            'websites' => $this->sites->all($this->server->id(), $this->user($request)),
            'secret' => $this->session->pullFlash('secret'),
        ]);
    }

    /** @param callable(): string $fn */
    private function act(Request $request, callable $fn): Response
    {
        try {
            return $this->redirectWithSuccess('/redis', $fn());
        } catch (ValidationException $e) {
            return $this->backWithErrors('/redis', $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/redis', $e);
        }
    }

    public function configure(Request $request): Response
    {
        return $this->act($request, function () use ($request): string {
            $this->redis->configure((int) $request->input('maxmemory_mb', 0), $request->string('policy'));
            return 'Redis settings saved to redis.conf.';
        });
    }

    public function password(Request $request): Response
    {
        return $this->act($request, function () use ($request): string {
            $clear = $request->input('clear') === '1';
            $pw = $this->redis->password($clear);
            if (!$clear) {
                $this->session->flash('secret', ['title' => 'Redis password set', 'user' => 'default', 'password' => $pw]);
                return 'Password set. Update applications that connect without a user name.';
            }
            return 'Password removed. Redis only listens locally, but a password is recommended.';
        });
    }

    public function flush(Request $request): Response
    {
        return $this->act($request, function () use ($request): string {
            $db = (int) $request->input('db', 0);
            $this->redis->flush($db, $request->string('confirm'));
            return 'Database ' . $db . ' flushed.';
        });
    }

    public function createUser(Request $request): Response
    {
        return $this->act($request, function () use ($request): string {
            $res = $this->redis->createSiteUser((int) $request->input('website_id', 0));
            $this->session->flash('secret', ['title' => 'Redis user for key prefix ' . $res['prefix'], 'user' => $res['user'], 'password' => $res['password']]);
            return 'Redis user created. It can only use keys starting with ' . $res['prefix'];
        });
    }

    public function deleteUser(Request $request): Response
    {
        return $this->act($request, function () use ($request): string {
            $this->redis->deleteUser($request->string('user'));
            return 'Redis user removed.';
        });
    }
}
