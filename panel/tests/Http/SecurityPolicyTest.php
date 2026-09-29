<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Mail\Mailer;
use WhaCP\Security\PasswordPolicy;
use WhaCP\Tests\Support\ArrayMailer;
use WhaCP\Tests\Support\HttpTestCase;

/** Security → Configure Security Policies and Password Strength Configuration. */
final class SecurityPolicyTest extends HttpTestCase
{
    private ArrayMailer $mailer;

    protected function setUp(): void
    {
        parent::setUp();
        $this->mailer = new ArrayMailer();
        $this->app->container->instance(Mailer::class, $this->mailer);
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
    }

    private function db(): Connection
    {
        return $this->app->container->get(Connection::class);
    }

    /** A fresh browser (new session) from an address. */
    private function browser(string $ip): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->app->container->instance(Mailer::class, $this->mailer);
        $this->remoteIp = $ip;
    }

    public function testStrengthScore(): void
    {
        self::assertLessThan(PasswordPolicy::STRONG, PasswordPolicy::strength('password1234'));
        self::assertLessThan(PasswordPolicy::STRONG, PasswordPolicy::strength('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'), 'repetition counts less');
        self::assertGreaterThanOrEqual(PasswordPolicy::STRONG, PasswordPolicy::strength('Sunflower!2024'));
    }

    public function testPasswordStrengthConfigurationIsEnforced(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/security/password-strength')->body();
        foreach (['Number of special characters', 'Number of uppercase characters', 'name="password_min_strength"', 'value="65"', 'password1234'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/security/password-strength', ['password_min_strength' => '70', 'password_min_length' => '14', 'password_min_special' => '2', 'password_min_numeric' => '1', 'password_min_alpha' => '4', 'password_min_upper' => '1', 'password_min_lower' => '1']);
        self::assertSame(2, $this->app->container->get(\WhaCP\Services\SettingsService::class)->int('password_min_special'));
        // Panel users.
        $this->post('/profile/password', ['current_password' => 'a-long-test-password', 'password' => 'Sunflower-2024x', 'password_confirmation' => 'Sunflower-2024x']);
        self::assertStringContainsString('2 special characters', (string) $this->flash('error') . json_encode($this->session->get('_flash')));
        $res = $this->post('/profile/password', ['current_password' => 'a-long-test-password', 'password' => 'Sunflower!-2024x', 'password_confirmation' => 'Sunflower!-2024x']);
        self::assertNotNull($this->flash('success'), 'a password meeting the rules is accepted');
        // The rules are shown on password forms.
        self::assertStringContainsString('At least 14 characters, 2 special', $this->request('GET', '/profile')->body());
        // Out of range values are refused.
        $this->post('/security/password-strength', ['password_min_strength' => '101']);
        self::assertSame(70, $this->app->container->get(\WhaCP\Services\SettingsService::class)->int('password_min_strength'));
        // Generated passwords meet the policy.
        $policy = $this->app->container->get(PasswordPolicy::class);
        self::assertNull($policy->check($policy->generate(20, ':')));
    }

    public function testPasswordStrengthCanBeTurnedOff(): void
    {
        $this->login('admin');
        $this->post('/security/policies', ['policy_password_strength' => '0', 'policy_verified_ips' => '0', 'policy_password_age_days' => '0']);
        self::assertNull($this->app->container->get(PasswordPolicy::class)->check('password1234x'), 'only the base rules');
        self::assertNotNull($this->app->container->get(PasswordPolicy::class)->check('short'));
    }

    public function testTwoFactorPolicyPerRole(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/security/policies')->body();
        foreach (['Limit logins to verified IP addresses', 'Two-Factor Authentication: Google Authenticator', 'Password Strength', 'Password Age', 'WhaCP webmail'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $customer = (int) $this->db()->scalar("SELECT id FROM roles WHERE name = 'customer'");
        $this->post('/security/policies', ['policy_verified_ips' => '0', 'policy_password_strength' => '1', 'policy_password_age_days' => '0', 'two_factor_roles' => [(string) $customer]]);
        self::assertSame(1, (int) $this->db()->scalar('SELECT require_two_factor FROM roles WHERE id = ?', [$customer]));
        self::assertSame(0, (int) $this->db()->scalar("SELECT require_two_factor FROM roles WHERE name = 'admin'"));
        $this->browser('192.0.2.10');
        $this->login('cust');
        self::assertSame('/profile#two-factor', $this->request('GET', '/websites')->header('Location'));
    }

    public function testPasswordAge(): void
    {
        $this->login('admin');
        $this->post('/security/policies', ['policy_verified_ips' => '0', 'policy_password_strength' => '1', 'policy_password_age_days' => '30']);
        $this->db()->run("UPDATE users SET password_changed_at = ? WHERE username = 'cust'", [gmdate('c', time() - 31 * 86400)]);
        $this->browser('192.0.2.10');
        $this->login('cust');
        self::assertSame('/profile#password', $this->request('GET', '/websites')->header('Location'));
        self::assertSame(200, $this->request('GET', '/profile')->status(), 'the profile stays reachable');
        $this->post('/profile/password', ['current_password' => 'a-long-test-password', 'password' => 'Brand-new-Passw0rd!', 'password_confirmation' => 'Brand-new-Passw0rd!']);
        self::assertSame(200, $this->request('GET', '/websites')->status(), 'a new password lifts it');
    }

    public function testLoginsLimitedToVerifiedAddresses(): void
    {
        // A sign-in before the policy: that address is remembered.
        $this->login('cust');
        $this->browser('192.0.2.10');
        $this->login('admin');
        $this->post('/security/policies', ['policy_verified_ips' => '1', 'policy_password_strength' => '1', 'policy_password_age_days' => '0']);
        self::assertStringContainsString('192.0.2.10', $this->request('GET', '/security/policies')->body());

        // Known address: signs in directly.
        $this->browser('192.0.2.10');
        $this->login('cust');

        // New address: a code by email, and no session until it is given.
        $this->browser('198.51.100.7');
        $res = $this->post('/login', ['username' => 'cust', 'password' => 'a-long-test-password']);
        self::assertSame('/login/verify-ip', $res->header('Location'));
        self::assertSame('/login', $this->request('GET', '/websites')->header('Location'), 'not signed in yet');
        self::assertSame(['cust@example.test'], end($this->mailer->sent)['to']);
        self::assertSame(1, preg_match('/code within 15 minutes: (\d{6})/', end($this->mailer->sent)['text'], $m));
        self::assertStringContainsString('198.51.100.7', end($this->mailer->sent)['text']);
        $this->post('/login/verify-ip', ['code' => '000000' === $m[1] ? '111111' : '000000']);
        self::assertSame('/login', $this->request('GET', '/websites')->header('Location'), 'a wrong code does not sign in');
        $res = $this->post('/login/verify-ip', ['code' => $m[1]]);
        self::assertSame('/', $res->header('Location'));
        self::assertSame(200, $this->request('GET', '/websites')->status());
        self::assertNotNull($this->db()->scalar("SELECT id FROM login_ips WHERE ip = '198.51.100.7'"));

        // The code only works from the address it was sent for.
        $this->browser('203.0.113.9');
        $this->post('/login', ['username' => 'cust', 'password' => 'a-long-test-password']);
        preg_match('/code within 15 minutes: (\d{6})/', end($this->mailer->sent)['text'], $m2);
        $this->remoteIp = '203.0.113.10';
        $this->post('/login/verify-ip', ['code' => $m2[1]]);
        $this->remoteIp = '203.0.113.9';
        self::assertSame('/login', $this->request('GET', '/websites')->header('Location'));

        // Without email, a new address cannot sign in.
        $this->db()->run("UPDATE users SET email = '' WHERE username = 'cust'");
        $this->browser('203.0.113.50');
        $this->post('/login', ['username' => 'cust', 'password' => 'a-long-test-password']);
        self::assertStringContainsString('no email address', (string) $this->flash('error'));
    }

    public function testOnlyAdministratorsChangePolicies(): void
    {
        $this->login('cust');
        self::assertNotSame(200, $this->request('GET', '/security/policies')->status());
        $this->post('/security/policies', ['policy_verified_ips' => '1']);
        $this->post('/security/password-strength', ['password_min_strength' => '0']);
        $settings = $this->app->container->get(\WhaCP\Services\SettingsService::class);
        self::assertFalse($settings->bool('policy_verified_ips'));
        self::assertSame(65, $settings->int('password_min_strength'));
    }

    public function testOverviewChecks(): void
    {
        $this->agent->succeed('security.audit', [
            ['id' => 'mysql_anonymous', 'title' => 'MySQL anonymous users', 'status' => 'fail', 'severity' => 'high', 'detail' => '1 anonymous account(s) can connect without a user name.', 'remedy' => 'Remove the anonymous users (button above).'],
            ['id' => 'database_version', 'title' => 'Supported database', 'status' => 'pass', 'severity' => 'high', 'detail' => 'The system is running a supported database: MariaDB 10.11 (supported until 2028-02-16).'],
            ['id' => 'nobody_mail', 'title' => '“nobody” may not send email', 'status' => 'warn', 'severity' => 'medium', 'detail' => 'x', 'remedy' => 'Turn on SMTP Restrictions.'],
            ['id' => 'ssh_version', 'title' => 'SSH version', 'status' => 'pass', 'severity' => 'medium', 'detail' => 'The current SSH version is up to date.'],
        ]);
        $this->agent->succeed('ssh.logins', ['logins' => []]);
        $this->agent->succeed('fail2ban.status', ['running' => true, 'jails' => []]);
        $this->agent->succeed('hardening.status', ['forkbomb' => ['enabled' => false], 'traceroute' => ['enabled' => true, 'programs' => [['path' => '/usr/bin/traceroute.db', 'mode' => '755', 'owner' => 'root:root']]], 'compilers' => [], 'smtp' => []]);
        $this->agent->succeed('db.drop_anonymous_users', ['removed' => ['localhost']]);
        $this->login('admin');
        $page = $this->request('GET', '/security')->body();
        foreach (['Password strength requirements are strong', 'Fork bomb protection is disabled', 'Traceroute is enabled for every user', 'Remove anonymous users', 'Supported database', 'SSH version', 'href="/security/smtp"', 'href="/security/forkbomb"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/security/mysql-anonymous');
        self::assertSame(1, $this->calls('db.drop_anonymous_users'));
        self::assertStringContainsString('localhost', (string) $this->flash('success'));
        $this->browser('192.0.2.10');
        $this->login('cust');
        $this->post('/security/mysql-anonymous');
        self::assertSame(1, $this->calls('db.drop_anonymous_users'), 'customers cannot');
    }
}
