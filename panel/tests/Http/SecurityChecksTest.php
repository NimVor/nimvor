<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Tests\Support\HttpTestCase;

/** On-demand security checks: file integrity, TLS audit, advisories. */
final class SecurityChecksTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->login('admin');
    }

    public function testPagesOpenWithoutCallingTheAgent(): void
    {
        $checkActions = ['security.integrity', 'tls.audit', 'packages.advisories'];
        foreach (['/security/integrity', '/security/tls', '/security/advisories'] as $url) {
            $before = count($this->agent->requests);
            $res = $this->request('GET', $url);
            self::assertSame(200, $res->status(), $url);
            self::assertStringContainsString($url . '/run', $res->body(), $url . ' has a run form');
            // The slow check action must not run on load (only on the Run button);
            // the page layout may still make its own light calls.
            $new = array_slice($this->agent->requests, $before);
            foreach ($checkActions as $a) {
                self::assertSame([], array_filter($new, static fn (array $c): bool => $c['path'] === '/v1/actions/' . $a), $url . ' called ' . $a . ' on load');
            }
        }
    }

    public function testRunIntegrityShowsFindings(): void
    {
        $this->agent->succeed('security.integrity', ['has_baseline' => true, 'findings' => [
            ['path' => '/usr/sbin/nginx', 'class' => 'package', 'issue' => 'modified', 'severity' => 'high', 'detail' => 'differs (5)'],
        ]]);
        $body = $this->post('/security/integrity/run', [])->body();
        self::assertStringContainsString('/usr/sbin/nginx', $body);
        self::assertStringContainsString('HIGH', $body);
    }

    public function testRunAdvisoriesShowsSecurityCount(): void
    {
        $this->agent->succeed('packages.advisories', ['count' => 1, 'security' => 1, 'normal' => 0, 'unknown' => 0, 'advisories' => [
            ['package' => 'openssl', 'installed' => '3.0.1', 'fixed' => '3.0.2', 'kind' => 'security', 'severity' => 'important', 'advisory' => 'RHSA-2026:1', 'cves' => ['CVE-2026-1']],
        ]]);
        $body = $this->post('/security/advisories/run', [])->body();
        self::assertStringContainsString('openssl', $body);
        self::assertStringContainsString('CVE-2026-1', $body);
    }

    public function testTlsWithNoHttpsSitesShowsNothingToAudit(): void
    {
        // No HTTPS websites and mail not configured → the run button is disabled.
        $this->agent->succeed('mail.status', ['configured' => false]);
        $body = $this->request('GET', '/security/tls')->body();
        self::assertStringContainsString('No HTTPS websites', $body);
    }

    public function testSetBaselineNeedsManagePermission(): void
    {
        $this->seedUser('viewer', 'viewer');
        $this->login('viewer');
        $res = $this->post('/security/integrity/baseline', []);
        self::assertSame(403, $res->status());
    }
}
