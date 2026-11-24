<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ApiTokenService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\Tests\Support\HttpTestCase;

/** API tokens: only administrators and resellers, and each token limited to the permissions it was given. */
final class ApiTokenScopesTest extends HttpTestCase
{
    private Connection $db;
    private ApiTokenService $tokens;
    private int $admin;
    private int $reseller;
    private int $customer;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->reseller = $this->seedUser('resell', 'reseller');
        $this->customer = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->tokens = $this->app->container->get(ApiTokenService::class);
    }

    private function api(string $method, string $path, string $token): Response
    {
        return $this->app->handle(new Request($method, $path, [], [], ['REMOTE_ADDR' => '192.0.2.70'], ['authorization' => 'Bearer ' . $token]));
    }

    /** @param array<string, mixed> $more */
    private function create(int $user, array $more): string
    {
        return $this->tokens->create($user, $more + ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => 'write', 'expires_days' => 30]);
    }

    public function testATokenMayOnlyCallEndpointsItsPermissionsCover(): void
    {
        $t = $this->create($this->admin, ['scopes' => ['websites.manage']]);
        $stored = (array) json_decode((string) $this->db->scalar('SELECT scopes FROM api_tokens WHERE user_id = ?', [$this->admin]), true);
        self::assertSame(['websites.manage', 'websites.view'], $stored, 'managing includes viewing');

        self::assertSame(200, $this->api('GET', '/api/v1/websites', $t)->status());
        self::assertSame(200, $this->api('GET', '/api/v1/me', $t)->status(), 'endpoints that need no permission stay open');
        $res = $this->api('GET', '/api/v1/dns/zones', $t);
        self::assertSame(403, $res->status(), $res->body());
        $e = (array) json_decode($res->body(), true)['error'];
        self::assertSame(['scope_missing', 'dns.manage'], [$e['code'], $e['permission']]);
        self::assertSame(403, $this->api('GET', '/api/v1/security/firewall', $t)->status());
    }

    public function testEverythingKeepsTheOldMeaning(): void
    {
        $t = $this->create($this->admin, ['all_scopes' => '1']);
        self::assertNull($this->db->scalar('SELECT scopes FROM api_tokens WHERE user_id = ?', [$this->admin]));
        self::assertSame(200, $this->api('GET', '/api/v1/websites', $t)->status());
        self::assertSame(200, $this->api('GET', '/api/v1/dns/zones', $t)->status());
    }

    public function testAScopeIsNeverBeyondTheUsersOwnPermissions(): void
    {
        // A reseller does not hold security.manage; asking for it alone leaves nothing to grant.
        try {
            $this->create($this->reseller, ['scopes' => ['security.manage', 'no.such.permission']]);
            self::fail('a token with nothing the user may grant was created');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('scopes', $e->errors());
        }
        $this->create($this->reseller, ['scopes' => ['security.manage', 'websites.view']]);
        self::assertSame(['websites.view'], json_decode((string) $this->db->scalar('SELECT scopes FROM api_tokens WHERE user_id = ?', [$this->reseller]), true));
    }

    public function testChoosingNothingIsRefused(): void
    {
        foreach ([[], ['scopes' => []], ['scopes' => 'websites.view']] as $i => $in) {
            try {
                $this->create($this->admin, $in);
                self::fail("case $i created a token");
            } catch (ValidationException $e) {
                self::assertArrayHasKey('scopes', $e->errors(), "case $i");
            }
        }
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM api_tokens'));
    }

    public function testHostingCustomersHaveNoApi(): void
    {
        try {
            $this->create($this->customer, ['all_scopes' => '1']);
            self::fail('a hosting customer got a token');
        } catch (UserFacingException $e) {
            self::assertSame(403, $e->status());
        }
        // A token that already exists (made before this rule) is refused too.
        $raw = 'nimvor_' . bin2hex(random_bytes(20));
        $this->db->run('INSERT INTO api_tokens (user_id, name, token_hash, prefix, ability, expires_at, created_at) VALUES (?, ?, ?, ?, ?, NULL, ?)', [$this->customer, 'old', hash('sha256', $raw), substr($raw, 0, 12), 'write', gmdate('c')]);
        $res = $this->api('GET', '/api/v1/websites', $raw);
        self::assertSame(403, $res->status());
        self::assertSame('api_not_available', ((array) json_decode($res->body(), true)['error'])['code']);
    }

    public function testTheProfileShowsTabsAndTheGroupedPermissionsToAdministratorsAndResellers(): void
    {
        foreach (['admin', 'resell'] as $who) {
            $this->session = new \NimVor\Http\Session\ArraySession();
            $this->app = $this->makeApp();
            $this->login($who);
            $html = $this->request('GET', '/profile')->body();
            self::assertStringContainsString('data-nim-hash-tabs', $html, $who);
            foreach (['#account', '#security', '#api'] as $pane) {
                self::assertStringContainsString('data-bs-target="' . $pane . '"', $html, "$who $pane");
            }
            self::assertStringContainsString('name="all_scopes"', $html);
            self::assertStringContainsString('value="websites.manage"', $html);
            self::assertStringContainsString('value="websites.view"', $html);
            self::assertStringNotContainsString('value="api.tokens"', $html, 'a token cannot make tokens');
            self::assertStringNotContainsString('<script>', $html);
        }
        // The administrator can also grant server permissions the reseller does not have.
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('resell');
        self::assertStringNotContainsString('value="security.manage"', $this->request('GET', '/profile')->body());
    }

    public function testACustomersProfileHasNoApiTab(): void
    {
        $this->login('cust');
        $html = $this->request('GET', '/profile')->body();
        self::assertStringNotContainsString('data-bs-target="#api"', $html);
        self::assertStringContainsString('data-bs-target="#security"', $html);
        $res = $this->post('/profile/tokens', ['name' => 'x', 'ability' => 'read', 'all_scopes' => '1', 'expires_days' => '30']);
        self::assertNotSame(200, $res->status());
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM api_tokens'));
    }

    public function testCreatingATokenFromThePageStoresTheChosenPermissions(): void
    {
        $this->login('admin');
        $res = $this->post('/profile/tokens', ['name' => 'billing', 'ability' => 'read', 'expires_days' => '30', 'scopes' => ['users.manage', 'plans.manage']]);
        self::assertSame(303, $res->status(), $res->body());
        self::assertSame('/profile#api', $res->header('location'));
        $stored = (array) json_decode((string) $this->db->scalar("SELECT scopes FROM api_tokens WHERE name = 'billing'"), true);
        self::assertSame(['plans.manage', 'users.manage', 'users.view'], $stored);
        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'api.token_create'");
        self::assertStringContainsString('users.manage', $audit);
    }
}
