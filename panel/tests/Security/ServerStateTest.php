<?php

declare(strict_types=1);

namespace WhaCP\Tests\Security;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentHttpResponse;
use WhaCP\Agent\AgentUnavailableException;
use WhaCP\Database\Connection;
use WhaCP\Services\ServerService;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * Multi-server failures: an agent that cannot be used is never recorded as
 * online, and the recorded state says why.
 */
final class ServerStateTest extends HttpTestCase
{
    private function state(callable $setup): string
    {
        $db = $this->app->container->get(Connection::class);
        $db->run("DELETE FROM servers WHERE id <> 1");
        $db->run("INSERT INTO servers (id, name, kind, url, status, created_at) VALUES (7, 'edge', 'remote', 'https://192.0.2.7:8443', 'online', ?)", [gmdate('c')]);
        $this->agent->down = false;
        unset($this->agent->responses['/v1/health']);
        $setup();
        $this->app->container->get(ServerService::class)->check(7, $this->app->container->get(AgentClient::class));
        return (string) $db->scalar('SELECT status FROM servers WHERE id = 7');
    }

    public function testStates(): void
    {
        $version = (string) $this->app->container->get(\WhaCP\Core\Config::class)->get('app.version');
        $health = fn (string $v): AgentHttpResponse => new AgentHttpResponse(200, (string) json_encode(['success' => true, 'data' => ['status' => 'ok', 'version' => $v], 'request_id' => 't']));
        self::assertSame('online', $this->state(fn () => $this->agent->responses['/v1/health'] = $health($version)));
        self::assertSame('version_mismatch', $this->state(fn () => $this->agent->responses['/v1/health'] = $health('0.1.0')));
        self::assertSame('offline', $this->state(fn () => $this->agent->down = true));
        self::assertSame('auth_failed', $this->state(fn () => $this->agent->responses['/v1/health'] = new AgentHttpResponse(401, (string) json_encode(['success' => false, 'error' => ['code' => 'unauthorized', 'message' => 'bad token']]))));
        self::assertNotSame('online', $this->state(fn () => $this->agent->responses['/v1/health'] = new AgentHttpResponse(200, 'not json at all')), 'a malformed answer is not healthy');
        self::assertNotSame('online', $this->state(fn () => $this->agent->responses['/v1/health'] = new AgentHttpResponse(500, '')), 'a server error is not healthy');
        foreach (['agent_pin_mismatch' => 'certificate_mismatch', 'agent_forbidden' => 'refused', 'agent_timeout' => 'timeout', 'agent_unavailable' => 'offline'] as $code => $want) {
            self::assertSame($want, ServerService::failureState(new AgentUnavailableException('x', '', '', $code)));
        }
    }
}
