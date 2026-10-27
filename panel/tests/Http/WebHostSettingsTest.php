<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Mail\MailConfig;
use WhaCP\Services\SettingsService;
use WhaCP\Tests\Support\HttpTestCase;

/** Basic WebHost Settings and Tweak Settings. */
final class WebHostSettingsTest extends HttpTestCase
{
    private SettingsService $settings;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('oper', 'operator');
        // Settings and security managers who are not administrators.
        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO roles (name, label, description, is_system, scope, created_at) VALUES ('settingsmgr', 'Settings', '', 0, 'all', ?)", [gmdate('c')]);
        $db->run("INSERT INTO role_permissions (role_id, permission_id) SELECT (SELECT id FROM roles WHERE name = 'settingsmgr'), id FROM permissions WHERE name IN ('dashboard.view', 'settings.view', 'settings.manage', 'security.view', 'security.manage')");
        $this->seedUser('setter', 'settingsmgr');
        $this->settings = $this->app->container->get(SettingsService::class);
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'server.example.com', 'interfaces' => [['name' => 'eth0', 'addresses' => ['203.0.113.10/24', '127.0.0.1/8', 'fe80::1/64']]]]]);
        $this->agent->succeed('hulk.sync', ['ok' => true]);
    }

    private function fresh(string $key): mixed
    {
        return $this->app->container->get(SettingsService::class)->get($key);
    }

    public function testTweakPageShowsDefaultsAndChangedSettings(): void
    {
        $this->settings->update(['session_idle_minutes' => '45'], null);
        $this->login('admin');
        $page = $this->request('GET', '/settings/tweak')->body();
        foreach (['general', 'accounts', 'dns', 'ssl', 'monitoring', 'security', 'policy', 'hulk'] as $tab) {
            self::assertStringContainsString('id="' . $tab . '" role="tabpanel"', $page);
        }
        self::assertMatchesRegularExpression('/data-setting="session_idle_minutes" data-changed="1"/', $page);
        self::assertMatchesRegularExpression('/data-setting="panel_name" data-changed="0"/', $page);
        self::assertStringContainsString('Default: <span class="fw-medium">30</span>', $page);
        self::assertStringContainsString('name="reset" value="session_idle_minutes"', $page);
        // Internal state is not listed; settings with their own page are read-only here.
        self::assertStringNotContainsString('data-setting="hulk_synced"', $page);
        self::assertStringNotContainsString('data-setting="server_setup"', $page);
        self::assertStringNotContainsString('name="hulk_enabled"', $page);
        self::assertStringContainsString('data-setting="hulk_enabled"', $page);
    }

    public function testSaveResetAndResetTab(): void
    {
        $this->login('admin');
        $this->post('/settings/tweak', ['tab' => 'general', 'panel_name' => 'Hosting Co', 'discovery_cache_seconds' => '60', 'session_idle_minutes' => '99']);
        self::assertSame('Hosting Co', $this->fresh('panel_name'));
        self::assertSame(60, $this->fresh('discovery_cache_seconds'));
        self::assertSame(30, $this->fresh('session_idle_minutes'), 'a setting of another tab is not taken from this form');
        self::assertStringContainsString('Saved: Panel name, Discovery cache', (string) $this->flash('success'));

        $this->post('/settings/tweak', ['tab' => 'general', 'reset' => 'panel_name']);
        self::assertSame('WhaCP', $this->fresh('panel_name'));
        self::assertSame(60, $this->fresh('discovery_cache_seconds'), 'only that setting');
        $this->post('/settings/tweak', ['tab' => 'general', 'reset' => 'all']);
        self::assertSame(300, $this->fresh('discovery_cache_seconds'));
        // A reset of a setting of another tab, or an unknown one, is refused.
        $this->settings->update(['session_idle_minutes' => '45'], null);
        $this->post('/settings/tweak', ['tab' => 'general', 'reset' => 'session_idle_minutes']);
        self::assertSame(45, $this->fresh('session_idle_minutes'));
        // Invalid values are reported and nothing of the tab is saved.
        $this->post('/settings/tweak', ['tab' => 'general', 'panel_name' => 'X', 'dashboard_refresh_seconds' => '1']);
        self::assertSame('WhaCP', $this->fresh('panel_name'));
        // Choices are checked.
        $this->post('/settings/tweak', ['tab' => 'accounts', 'account_mail_routing' => 'pigeon', 'default_php_version' => '8.3; x']);
        self::assertSame('auto', $this->fresh('account_mail_routing'));
        self::assertSame('', $this->fresh('default_php_version'));
    }

    public function testSecurityPoliciesAndBruteForceNeedAnAdministrator(): void
    {
        $this->login('setter');
        $this->post('/settings/tweak', ['tab' => 'policy', 'password_min_length' => '20']);
        self::assertSame(12, $this->fresh('password_min_length'));
        $this->post('/settings/tweak', ['tab' => 'hulk', 'hulk_ip_command' => 'touch /tmp/x']);
        self::assertSame('', $this->fresh('hulk_ip_command'));
        self::assertStringContainsString('only administrators', (string) $this->flash('error'));
        // Other tabs they may change.
        $this->post('/settings/tweak', ['tab' => 'security', 'session_idle_minutes' => '40']);
        self::assertSame(40, $this->fresh('session_idle_minutes'));
        // Without settings.manage nothing is changed.
        $this->login('oper');
        $this->post('/settings/tweak', ['tab' => 'general', 'panel_name' => 'Nope']);
        self::assertSame('WhaCP', $this->fresh('panel_name'));
    }

    public function testBruteForceTabAppliesItsRulesAndKeepsLockoutSettingsOnItsPage(): void
    {
        $this->login('admin');
        $this->post('/settings/tweak', ['tab' => 'hulk', 'hulk_enabled' => '1', 'hulk_ip_max' => '9', 'hulk_user_scope' => 'local']);
        self::assertFalse($this->fresh('hulk_enabled'), 'turned on only on its page (which whitelists your address)');
        self::assertSame(9, $this->fresh('hulk_ip_max'));
        self::assertSame('local', $this->fresh('hulk_user_scope'));
        self::assertSame(1, $this->calls('hulk.sync'));
        $this->post('/settings/tweak', ['tab' => 'hulk', 'hulk_user_scope' => 'everyone']);
        self::assertSame('local', $this->fresh('hulk_user_scope'));
        $this->post('/settings/tweak', ['tab' => 'policy', 'policy_verified_ips' => '1']);
        self::assertFalse($this->fresh('policy_verified_ips'));
    }

    public function testBasicSettings(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/settings/basic')->body();
        self::assertStringContainsString('server.example.com', $page);
        self::assertStringContainsString('<option value="203.0.113.10">', $page);
        self::assertStringNotContainsString('<option value="127.0.0.1">', $page);

        $mail = $this->app->container->get(MailConfig::class);
        $mail->save(['host' => 'smtp.example.com', 'port' => '587', 'encryption' => 'starttls', 'username' => 'u', 'password' => 'secret-pass', 'from' => 'panel@example.com', 'alert_to' => ''], null);
        $this->post('/settings/basic', ['tab' => 'contact', 'alert_to' => 'ops@example.com, boss@example.com']);
        $m = $mail->get();
        self::assertSame('ops@example.com, boss@example.com', $m['alert_to']);
        self::assertSame('smtp.example.com', $m['host'], 'the mail settings are kept');
        self::assertTrue($m['has_password'], 'the stored password too');

        $this->post('/settings/basic', ['tab' => 'nameservers', 'ns1' => 'NS1.Example.com.', 'ns2' => 'ns2.example.com', 'ns3' => '', 'ns4' => '']);
        self::assertSame('ns1.example.com, ns2.example.com', $this->fresh('dns_nameservers'));
        $this->post('/settings/basic', ['tab' => 'nameservers', 'ns1' => 'not a name', 'ns2' => '']);
        self::assertSame('ns1.example.com, ns2.example.com', $this->fresh('dns_nameservers'));

        $this->post('/settings/basic', ['tab' => 'basic', 'panel_name' => 'Acme', 'panel_url' => 'https://panel.example.com', 'dns_default_ipv4' => '203.0.113.10', 'dns_hostmaster' => '', 'session_idle_minutes' => '5']);
        self::assertSame('Acme', $this->fresh('panel_name'));
        self::assertSame('203.0.113.10', $this->fresh('dns_default_ipv4'));
        self::assertSame(30, $this->fresh('session_idle_minutes'), 'only the section\'s settings');
    }

    public function testNewAccountFormStartsWithTheDefaults(): void
    {
        $this->login('admin');
        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO plans (name, description, created_at, updated_at) VALUES ('Gold', '', ?, ?)", [gmdate('c'), gmdate('c')]);
        $plan = (int) $db->scalar("SELECT id FROM plans WHERE name = 'Gold'");
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.2', 'fpm_binary' => '/x'], ['version' => '8.3', 'fpm_binary' => '/y']]]);
        $this->post('/settings/basic', ['tab' => 'accounts', 'default_plan_id' => (string) $plan, 'default_php_version' => '8.2', 'account_dns_dmarc' => '0', 'account_spam_box' => '0', 'account_dns_dkim' => '1', 'account_dns_spf' => '1', 'account_spam_filter' => '1', 'account_mail_routing' => 'remote']);
        self::assertSame($plan, $this->fresh('default_plan_id'));
        $form = $this->request('GET', '/accounts/new')->body();
        self::assertMatchesRegularExpression('/<option value="' . $plan . '" selected>Gold/', $form);
        self::assertStringContainsString('<option value="8.2" selected>PHP 8.2', $form);
        self::assertMatchesRegularExpression('/name="mail_routing"[^>]*value="remote"[^>]*checked|value="remote"[^>]*checked/', $form);
        $site = $this->request('GET', '/websites/new')->body();
        self::assertStringContainsString('<option value="8.2" selected>PHP 8.2', $site);
        // A default that is not installed: the newest installed.
        $this->settings->update(['default_php_version' => '8.4'], null);
        self::assertStringContainsString('<option value="8.3" selected>PHP 8.3', $this->request('GET', '/websites/new')->body());
    }

    public function testSecurityPoliciesNeedARecentPasswordConfirmation(): void
    {
        $this->login('admin');
        $this->session->set('_auth_confirmed_at', time() - 3600);
        $res = $this->post('/settings/tweak', ['tab' => 'policy', 'password_min_length' => '20']);
        self::assertStringStartsWith('/confirm-password', (string) $res->header('Location'));
        self::assertSame(12, $this->fresh('password_min_length'));
        // Other tabs are not affected.
        $this->post('/settings/tweak', ['tab' => 'general', 'panel_name' => 'Acme']);
        self::assertSame('Acme', $this->fresh('panel_name'));
        $this->session->set('_auth_confirmed_at', time());
        $this->post('/settings/tweak', ['tab' => 'policy', 'password_min_length' => '20']);
        self::assertSame(20, $this->fresh('password_min_length'));
    }
}
