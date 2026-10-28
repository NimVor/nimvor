<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Agent\AgentHttpResponse;
use NimVor\Agent\AgentTransport;
use NimVor\Agent\RemoteTransport;
use NimVor\Agent\RoutingTransport;
use NimVor\Database\Connection;
use NimVor\Security\Crypto;
use NimVor\Services\ServerContext;
use NimVor\Tests\Support\FakeTransport;
use NimVor\Tests\Support\HttpTestCase;

final class ServersTest extends HttpTestCase
{
    public function testRoutingUsesTheSelectedServersTokenAndPin(): void
    {
        $db = $this->app->container->get(Connection::class);
        $crypto = $this->app->container->get(Crypto::class);
        $db->run("INSERT INTO servers (id, name, kind, url, token_enc, fingerprint, created_at) VALUES (2, 'web2', 'remote', 'https://203.0.113.10:8443', ?, 'sha256//pin', '2026-01-01')", [$crypto->encrypt('remote-token')]);
        $ctx = new ServerContext(new \NimVor\Http\Session\ArraySession());
        $local = new FakeTransport();
        $remote = new FakeTransport();
        $made = [];
        $routing = new RoutingTransport($local, $ctx, $db, $crypto, function (string $url, string $pin) use ($remote, &$made): AgentTransport {
            $made[] = [$url, $pin];
            return $remote;
        });
        $routing->request('GET', '/v1/health', null, 'local-token', 5);
        self::assertSame('local-token', $local->requests[0]['token']);
        $ctx->force(2);
        $routing->request('GET', '/v1/health', null, 'local-token', 5);
        self::assertSame('remote-token', $remote->requests[0]['token'], 'the local token is never sent to a remote server');
        self::assertSame([['https://203.0.113.10:8443', 'sha256//pin']], $made);
        $ctx->force(3);
        $this->expectException(\NimVor\Agent\AgentUnavailableException::class);
        $routing->request('GET', '/v1/health', null, 'local-token', 5);
    }

    public function testValidationSwitchingAndRemoval(): void
    {
        self::assertTrue(RemoteTransport::validPin('sha256//n8lIFssNFex7vf0LcPXNFigGAnx+3vFllzzEN++9K2A='));
        self::assertFalse(RemoteTransport::validPin('n8lIFssNFex7vf0LcPXNFigGAnx'));
        self::assertTrue(RemoteTransport::validUrl('https://[2001:db8::1]:8443'));
        self::assertFalse(RemoteTransport::validUrl('http://203.0.113.10:8443'));
        self::assertFalse(RemoteTransport::validUrl('https://x.test/path'));

        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->post('/servers', ['name' => 'web2', 'url' => 'http://x', 'pin' => 'nope', 'token' => 'short']);
        $errors = (array) $this->flash('errors');
        self::assertArrayHasKey('url', $errors);
        self::assertArrayHasKey('pin', $errors);
        self::assertArrayHasKey('token', $errors);
        self::assertStringNotContainsString('short', (string) json_encode($this->session->pullFlash('old')), 'the token is not flashed back');

        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO servers (id, name, kind, url, token_enc, fingerprint, created_at) VALUES (2, 'web2', 'remote', 'https://203.0.113.10:8443', 'x', 'sha256//pin', '2026-01-01')");
        self::assertStringContainsString('name="server_id"', $this->request('GET', '/')->body(), 'switcher appears with two servers');
        $this->post('/servers/switch', ['server_id' => '2']);
        self::assertSame(2, $this->app->container->get(ServerContext::class)->id());
        $this->post('/servers/switch', ['server_id' => '99']);
        self::assertSame(2, $this->app->container->get(ServerContext::class)->id());

        $this->post('/servers/1/delete');
        self::assertSame(1, (int) $db->scalar('SELECT COUNT(*) FROM servers WHERE id = 1'), 'the local server cannot be removed');
        $this->post('/servers/2/delete');
        self::assertSame(0, (int) $db->scalar('SELECT COUNT(*) FROM servers WHERE id = 2'));
        self::assertSame(1, $this->app->container->get(ServerContext::class)->id(), 'switched back to the local server');
    }
}
