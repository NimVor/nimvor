<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Services\ServerSetupService;
use WhaCP\Services\SettingsService;
use WhaCP\Tests\Support\HttpTestCase;

/** The setup wizard: first visit, checklist, steps run one by one from the browser. */
final class SetupWizardTest extends HttpTestCase
{
    private const JOB = 'abcdef0123456789';

    protected function setUp(): void
    {
        parent::setUp();
        $this->app->container->get(SettingsService::class)->update(['setup_wizard_seen' => false, 'setup_wizard_done' => false], null);
        $this->seedUser('admin', 'admin');
        $this->seedUser('oper', 'operator');
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'web1'], 'os' => []]);
        $this->agent->succeed('system.metrics', ['memory' => ['total_bytes' => 8 * 1024 ** 3]]);
        $this->agent->succeed('components.status', [
            ['id' => 'web', 'name' => 'Web server', 'installed' => true, 'ready' => true, 'steps' => [['id' => 'installed', 'status' => 'ok']]],
            ['id' => 'mail', 'name' => 'Email', 'installed' => false, 'ready' => false, 'steps' => [['id' => 'installed', 'status' => 'todo', 'items' => ['postfix']]]],
        ]);
        $this->agent->succeed('packages.install', ['job_id' => self::JOB]);
        $this->agent->succeed('security.auto_updates', ['job_id' => self::JOB]);
        $this->agent->succeed('jobs.log', ['log' => '', 'offset' => 0, 'done' => true, 'job' => ['id' => self::JOB, 'status' => 'succeeded']]);
        $this->agent->succeed('jobs.get', ['id' => self::JOB, 'status' => 'succeeded']);
    }

    private function step(string $step, array $extra = []): array
    {
        $res = $this->post('/setup/step', $extra + ['profile' => 'full', 'hostname' => 'server.example.com', 'php' => ['8.3', '8.4'], 'third_party_repos' => '1', 'step' => $step]);
        return ['status' => $res->status()] + (array) json_decode($res->body(), true);
    }

    public function testFirstDashboardVisitOpensTheWizardOnce(): void
    {
        $this->login('admin');
        self::assertSame('/setup', $this->request('GET', '/')->header('Location'));
        $page = $this->request('GET', '/setup')->body();
        self::assertStringContainsString('Setup wizard', $page);
        // 8 GB of memory: the full profile is recommended and chosen.
        self::assertMatchesRegularExpression('/value="full" id="sp-full" checked/', $page);
        foreach (['setup', 'components', 'two_factor', 'remote_backups', 'backup_key', 'account'] as $c) {
            self::assertStringContainsString('data-check="' . $c . '" data-done="0"', $page, $c);
        }
        self::assertStringContainsString('Needs attention: Email.', $page);
        // Every step of each profile is listed; the full profile's shown.
        self::assertSame(count(ServerSetupService::steps('full')), substr_count(explode('</ol>', explode('data-setup-steps="full"', $page)[1])[0], 'data-step="'));
        // Then the dashboard itself, with a card back to the wizard.
        $dash = $this->request('GET', '/');
        self::assertSame(200, $dash->status());
        self::assertStringContainsString('data-setup-card', $dash->body());
        $this->post('/setup/dismiss');
        self::assertStringNotContainsString('data-setup-card', $this->request('GET', '/')->body());
        self::assertSame(200, $this->request('GET', '/setup')->status(), 'reopened any time');
    }

    public function testOperatorsAreNotSentToTheWizardAndCannotRunIt(): void
    {
        $this->login('oper');
        self::assertNotSame('/setup', $this->request('GET', '/')->header('Location'));
        $res = $this->step('packages');
        self::assertContains($res['status'], [302, 303, 403]);
        self::assertSame(0, $this->calls('packages.install'));
    }

    public function testStepThatStartsAJobIsFollowedThenVerified(): void
    {
        $this->login('admin');
        $res = $this->step('packages');
        self::assertSame('running', $res['step']['status']);
        self::assertSame(self::JOB, $res['step']['job_id']);
        self::assertSame(['names' => ['postfix']], $this->lastCall('packages.install'));
        // The job ended; the step checks again: nothing left means done.
        $this->agent->succeed('components.status', [['id' => 'mail', 'name' => 'Email', 'installed' => true, 'ready' => true, 'steps' => [['id' => 'installed', 'status' => 'ok']]]]);
        $res = $this->step('packages', ['after_job' => self::JOB, 'round' => '1']);
        self::assertSame('ok', $res['step']['status']);
        self::assertSame(1, $this->calls('packages.install'));
        $last = $this->app->container->get(ServerSetupService::class)->last();
        self::assertSame('full', $last['profile']);
        self::assertSame('wizard', $last['via']);
        self::assertSame('ok', $last['steps']['packages']['status']);
        self::assertNull($last['finished_at']);
    }

    public function testStepStillNotDoneAfterSeveralJobsFails(): void
    {
        $this->login('admin');
        $res = $this->step('packages', ['after_job' => self::JOB, 'round' => (string) ServerSetupService::MAX_ROUNDS]);
        self::assertSame('fail', $res['step']['status']);
        self::assertStringContainsString('Still not done', $res['step']['detail']);
    }

    public function testFailedJobFailsTheStep(): void
    {
        $this->login('admin');
        $this->agent->succeed('jobs.get', ['id' => self::JOB, 'status' => 'failed', 'error' => 'E: Unable to locate package postfix']);
        $res = $this->step('auto_updates', ['after_job' => self::JOB, 'round' => '1']);
        self::assertSame('fail', $res['step']['status']);
        self::assertStringContainsString('Unable to locate package', $res['step']['detail']);
    }

    public function testOneJobStepIsDoneWhenItsJobSucceeded(): void
    {
        $this->login('admin');
        self::assertSame('running', $this->step('auto_updates')['step']['status']);
        self::assertSame('ok', $this->step('auto_updates', ['after_job' => self::JOB, 'round' => '1'])['step']['status']);
        self::assertSame(1, $this->calls('security.auto_updates'), 'not started again');
    }

    public function testLastStepFinishesTheRun(): void
    {
        $this->login('admin');
        $this->step('packages');
        $res = $this->step('profile');
        self::assertSame('ok', $res['step']['status']);
        $last = $this->app->container->get(ServerSetupService::class)->last();
        self::assertNotNull($last['finished_at']);
        self::assertSame(implode(',', ServerSetupService::PROFILES['full']), $this->app->container->get(SettingsService::class)->string('components_expected'));
    }

    public function testInvalidInputIsRefused(): void
    {
        $this->login('admin');
        foreach ([['profile' => 'huge'], ['php' => ['7.0']], ['php' => ['8.3; rm -rf /']], ['hostname' => 'a b'], ['step' => 'mail', 'profile' => 'minimal'], ['step' => 'nope'], ['after_job' => '../x', 'step' => 'packages']] as $bad) {
            $res = $this->step((string) ($bad['step'] ?? 'packages'), $bad);
            self::assertSame(422, $res['status'], json_encode($bad));
        }
        self::assertSame(0, $this->calls('packages.install'));
    }

    public function testExistingInstallationsSkipTheFirstVisit(): void
    {
        $this->login('admin');
        $s = $this->app->container->get(SettingsService::class);
        // A setup already ran (the installer's profile): no redirect.
        $s->update(['server_setup' => '{"profile":"web","steps":{}}'], null);
        self::assertSame(200, $this->request('GET', '/')->status());
    }
}
