<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Sets up a server for a profile (Full, Web only, Minimal): the components'
 * packages, PHP, email, DNS, FTP, caches, firewall and its protections,
 * fail2ban, hardening, automatic security updates, hosting plans with
 * feature lists and weekly backups of every website.
 *
 * Every step is one of the panel's existing actions and is idempotent:
 * what is already in place is left alone (an existing plan, schedule,
 * mail or FTP setup is never changed), so the setup can run again at any
 * time. A failed step is reported and the next ones still run. Nothing is
 * removed: PHP versions and extensions already installed are kept.
 *
 * Firewall changes are guarded by the agent (undone after 2 minutes
 * unless kept). The setup keeps them itself, but only after reading the
 * firewall again and finding every critical port (SSH, the panel, the
 * agent's listener) open; otherwise it lets the change be undone.
 */
final class ServerSetupService
{
    /** Components each profile installs and sets up (Server → Components ids). */
    public const PROFILES = [
        'minimal' => ['web', 'php', 'database', 'firewall', 'fail2ban', 'certbot'],
        'web' => ['web', 'php', 'database', 'firewall', 'fail2ban', 'certbot', 'redis', 'memcached', 'ftp'],
        'full' => ['web', 'php', 'database', 'firewall', 'fail2ban', 'certbot', 'redis', 'memcached', 'ftp', 'mail', 'dns', 'clamav'],
    ];

    public const PROFILE_NAMES = ['full' => 'Full hosting server', 'web' => 'Web hosting only', 'minimal' => 'Minimal'];

    /** PHP versions installed by default; more can be chosen. */
    public const PHP_DEFAULT = ['8.3', '8.4'];

    public const STEPS = [
        'packages' => 'Install the components',
        'php_repository' => 'PHP repository',
        'php' => 'PHP versions and extensions',
        'mail' => 'Email',
        'dns' => 'DNS server',
        'ftp' => 'FTP server',
        'caches' => 'Redis memory limit',
        'services' => 'Start the services',
        'firewall' => 'Firewall',
        'ports' => 'Open the components\' ports',
        'protection' => 'Firewall protections',
        'fail2ban' => 'Fail2ban',
        'hardening' => 'Server hardening',
        'security_defaults' => 'Recommended security settings',
        'auto_updates' => 'Automatic security updates',
        'plans' => 'Hosting plans and feature lists',
        'backups' => 'Weekly backups',
        'profile' => 'Server profile',
    ];

    /** Steps that do not apply to every profile. */
    private const STEP_PROFILES = [
        'mail' => ['full'], 'dns' => ['full'], 'ftp' => ['web', 'full'], 'caches' => ['web', 'full'],
        'protection' => ['web', 'full'],
    ];

    /** Feature lists: name => [description, features turned on]. */
    public const FEATURE_LISTS = [
        'Hosting' => ['Websites without shell access.', ['php', 'mysql', 'email', 'dns', 'file_manager', 'ssl', 'subdomains', 'ftp', 'cron', 'backups', 'apps', 'git', 'workers', 'redis', 'logs', 'stats', 'malware', 'wordpress', 'addon_domains', 'cloudflare', 'firewall', 'memcached', 'mongodb', 'nodejs', 'python', 'site_health', 'site_security', 'site_access', 'web_policy', 'redirects', 'type_php', 'type_static']],
        'Hosting with SSH' => ['Every feature, the SSH shell included.', ['php', 'mysql', 'email', 'dns', 'file_manager', 'ssl', 'subdomains', 'ftp', 'cron', 'backups', 'apps', 'git', 'workers', 'redis', 'logs', 'stats', 'malware', 'wordpress', 'addon_domains', 'cloudflare', 'firewall', 'memcached', 'mongodb', 'nodejs', 'python', 'site_health', 'site_security', 'site_access', 'web_policy', 'redirects', 'type_php', 'type_static', 'ssh']],
        'Email only' => ['Email, DNS and certificates for a domain; no website tools.', ['email', 'dns', 'ssl', 'backups']],
    ];

    /**
     * Plans: name => [description, feature list, limits]. 0 = unlimited
     * (for resource limits: no limit).
     */
    public const PLANS = [
        'Starter' => ['One website for a small business or a blog.', 'Hosting', [
            'disk_quota_mb' => 5120, 'bandwidth_mb' => 51200, 'max_websites' => 1, 'max_subdomains' => 5, 'max_parked_domains' => 2, 'max_addon_domains' => 0,
            'max_databases' => 2, 'max_ftp_accounts' => 2, 'max_email_accounts' => 5, 'max_email_quota_mb' => 1024, 'max_cron_jobs' => 2, 'max_workers' => 0, 'max_customers' => 0,
            'cpu_percent' => 100, 'memory_mb' => 1024, 'tasks_max' => 128, 'io_read_mbps' => 0, 'io_write_mbps' => 0,
        ]],
        'Business' => ['A few websites with email for a company.', 'Hosting', [
            'disk_quota_mb' => 20480, 'bandwidth_mb' => 204800, 'max_websites' => 5, 'max_subdomains' => 20, 'max_parked_domains' => 10, 'max_addon_domains' => 4,
            'max_databases' => 10, 'max_ftp_accounts' => 10, 'max_email_accounts' => 25, 'max_email_quota_mb' => 2048, 'max_cron_jobs' => 10, 'max_workers' => 2, 'max_customers' => 0,
            'cpu_percent' => 200, 'memory_mb' => 2048, 'tasks_max' => 256, 'io_read_mbps' => 0, 'io_write_mbps' => 0,
        ]],
        'Pro' => ['Many websites, workers and SSH for developers and agencies.', 'Hosting with SSH', [
            'disk_quota_mb' => 51200, 'bandwidth_mb' => 0, 'max_websites' => 20, 'max_subdomains' => 50, 'max_parked_domains' => 20, 'max_addon_domains' => 19,
            'max_databases' => 50, 'max_ftp_accounts' => 50, 'max_email_accounts' => 100, 'max_email_quota_mb' => 5120, 'max_cron_jobs' => 25, 'max_workers' => 5, 'max_customers' => 0,
            'cpu_percent' => 400, 'memory_mb' => 4096, 'tasks_max' => 512, 'io_read_mbps' => 0, 'io_write_mbps' => 0,
        ]],
        'Email only' => ['Mailboxes for one domain, no website.', 'Email only', [
            'disk_quota_mb' => 10240, 'bandwidth_mb' => 51200, 'max_websites' => 1, 'max_subdomains' => 0, 'max_parked_domains' => 0, 'max_addon_domains' => 0,
            'max_databases' => 0, 'max_ftp_accounts' => 0, 'max_email_accounts' => 50, 'max_email_quota_mb' => 2048, 'max_cron_jobs' => 0, 'max_workers' => 0, 'max_customers' => 0,
            'cpu_percent' => 50, 'memory_mb' => 256, 'tasks_max' => 64, 'io_read_mbps' => 0, 'io_write_mbps' => 0,
        ]],
    ];

    /** Longest wait for one job (package installs can be slow). */
    private const JOB_TIMEOUT = 3600;

    /** @var callable(string): void */
    private $out;

    /** The administrator running the setup (the wizard); null from the command line. */
    public ?int $actorId = null;

    /** The wizard user's address (kept out of brute force blocks); null from the command line. */
    public ?string $actorIp = null;

    /** Between two reads of a job's log (tests set 0). */
    public int $pollMicroseconds = 1_000_000;

    public function __construct(
        private readonly AgentClient $agent,
        private readonly JobTracker $jobs,
        private readonly SettingsService $settings,
        private readonly PlanService $plans,
        private readonly FeatureService $features,
        private readonly BackupScheduler $scheduler,
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly SecurityDefaultsService $securityDefaults,
    ) {
        $this->out = static function (string $line): void {
        };
    }

    /** The steps a profile runs, in order. @return list<string> */
    public static function steps(string $profile): array
    {
        return array_values(array_filter(array_keys(self::STEPS), static fn (string $s): bool => !isset(self::STEP_PROFILES[$s]) || in_array($profile, self::STEP_PROFILES[$s], true)));
    }

    /**
     * The last setup run (for the setup wizard): profile, options, times and
     * each step's result.
     *
     * @return array<string, mixed>|null
     */
    public function last(): ?array
    {
        $v = json_decode($this->settings->string('server_setup'), true);
        return is_array($v) ? $v : null;
    }

    /**
     * Runs the setup for a profile.
     *
     * @param array{hostname?: string, php?: list<string>, third_party_repos?: bool, only?: list<string>} $options
     * @param callable(string): void|null $out progress lines
     * @return list<array{id: string, label: string, status: string, detail: string}>
     */
    public function run(string $profile, array $options = [], ?callable $out = null): array
    {
        $opts = $this->options($profile, $options);
        $this->out = $out ?? static function (string $line): void {
        };
        $only = $options['only'] ?? null;
        $state = ['profile' => $profile, 'options' => $opts, 'started_at' => gmdate('c'), 'finished_at' => null, 'steps' => []];
        $results = [];
        foreach (self::steps($profile) as $id) {
            if ($only !== null && !in_array($id, $only, true)) {
                continue;
            }
            $this->say('');
            $this->say('== ' . self::STEPS[$id]);
            [$status, $detail] = $this->call($profile, $opts, $id);
            $this->say(sprintf('   %s: %s', ['ok' => 'done', 'skip' => 'nothing to do', 'fail' => 'FAILED'][$status] ?? $status, $detail));
            $results[] = ['id' => $id, 'label' => self::STEPS[$id], 'status' => $status, 'detail' => mb_substr($detail, 0, 300)];
            $state['steps'][$id] = ['status' => $status, 'detail' => mb_substr($detail, 0, 300)];
            $this->save($state);
        }
        $state['finished_at'] = gmdate('c');
        $this->save($state);
        $failed = array_column(array_filter($results, static fn (array $r): bool => $r['status'] === 'fail'), 'id');
        $this->audit->record('server.setup', 'server', (string) $this->server->id(), $failed === [] ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['profile' => $profile, 'failed' => $failed]);
        return $results;
    }

    /**
     * Validated options.
     *
     * @param array<string, mixed> $options
     * @return array{hostname: string, php: list<string>, third_party_repos: bool}
     */
    public function options(string $profile, array $options): array
    {
        if (!isset(self::PROFILES[$profile])) {
            throw new ValidationException(['profile' => 'Choose full, web or minimal.']);
        }
        $php = array_values(array_unique(array_filter((array) ($options['php'] ?? self::PHP_DEFAULT), static fn ($v): bool => is_string($v) && (bool) preg_match('/^[5-9]\.[0-9]{1,2}$/', $v))));
        if ($php === [] || count($php) !== count((array) ($options['php'] ?? self::PHP_DEFAULT))) {
            throw new ValidationException(['php' => 'Choose at least one PHP version, e.g. 8.3,8.4.']);
        }
        usort($php, 'version_compare');
        $hostname = strtolower(trim((string) ($options['hostname'] ?? '')));
        if ($hostname !== '' && !preg_match('/^(?=.{4,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/', $hostname)) {
            throw new ValidationException(['hostname' => 'Enter the server\'s full host name, e.g. server.example.com.']);
        }
        return ['hostname' => $hostname, 'php' => $php, 'third_party_repos' => (bool) ($options['third_party_repos'] ?? true)];
    }

    /**
     * Runs one step, its exceptions turned into a failed result.
     *
     * @param array<string, mixed> $opts
     * @return array{0: string, 1: string}
     */
    private function call(string $profile, array $opts, string $id): array
    {
        try {
            return $this->{'step' . str_replace('_', '', ucwords($id, '_'))}($profile, $opts);
        } catch (SetupJobStarted $e) {
            throw $e;
        } catch (AgentException $e) {
            return ['fail', trim($e->getMessage() . ' ' . $e->reason() . ' ' . $e->remedy())];
        } catch (ValidationException $e) {
            return ['fail', implode(' ', $e->errors())];
        } catch (UserFacingException $e) {
            return ['fail', $e->getMessage()];
        }
    }

    /** Steps whose job is the whole step: done when the job succeeded. */
    private const ONE_JOB_STEPS = ['php_repository', 'auto_updates'];

    /** Jobs one step may start before it counts as stuck. */
    public const MAX_ROUNDS = 3;

    /**
     * One step for the setup wizard, which runs the steps from the browser:
     * a step that starts a job returns "running" with the job id; the
     * browser follows the job and calls the step again with that job id
     * (after_job), and the step then checks what is left. The result is
     * remembered with the profile.
     *
     * @param array<string, mixed> $options
     * @return array{id: string, label: string, status: string, detail: string, job_id?: string}
     */
    public function runStep(string $profile, array $options, string $id, ?string $afterJob = null, int $round = 0): array
    {
        $opts = $this->options($profile, $options);
        if (!in_array($id, self::steps($profile), true)) {
            throw new ValidationException(['step' => 'This step is not part of the profile.']);
        }
        $state = $this->last();
        $first = $id === self::steps($profile)[0] && $afterJob === null;
        if ($first || $state === null || ($state['profile'] ?? '') !== $profile || ($state['options'] ?? null) != $opts) {
            $state = ['profile' => $profile, 'options' => $opts, 'started_at' => gmdate('c'), 'finished_at' => null, 'steps' => [], 'via' => 'wizard'];
        }
        $this->attempted = $afterJob === null ? [] : array_values(array_map('strval', (array) ($state['attempted'][$id] ?? [])));
        $result = $this->stepAfter($profile, $opts, $id, $afterJob, $round);
        $state['attempted'][$id] = $this->attempted;
        if ($result['status'] === 'running') {
            $this->save($state);
        } else {
            $state['steps'][$id] = ['status' => $result['status'], 'detail' => $result['detail']];
            $steps = self::steps($profile);
            if ($id === end($steps)) {
                $state['finished_at'] = gmdate('c');
                $failed = array_keys(array_filter((array) $state['steps'], static fn (array $s): bool => $s['status'] === 'fail'));
                $this->audit->record('server.setup', 'server', (string) $this->server->id(), $failed === [] ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['profile' => $profile, 'failed' => $failed, 'via' => 'wizard']);
            }
            $this->save($state);
        }
        return ['id' => $id, 'label' => self::STEPS[$id]] + $result;
    }

    /**
     * @param array<string, mixed> $opts
     * @return array{status: string, detail: string, job_id?: string}
     */
    private function stepAfter(string $profile, array $opts, string $id, ?string $afterJob, int $round): array
    {
        if ($afterJob !== null) {
            if (!preg_match('/^[a-f0-9]{16}$/', $afterJob)) {
                throw new ValidationException(['after_job' => 'Unknown job.']);
            }
            $job = (array) $this->agent->call('jobs.get', ['id' => $afterJob]);
            $st = (string) ($job['status'] ?? '');
            if ($st !== 'succeeded' && $st !== 'failed') {
                return ['status' => 'running', 'detail' => 'Still running.', 'job_id' => $afterJob];
            }
            if ($st === 'failed') {
                return ['status' => 'fail', 'detail' => 'The job failed: ' . mb_substr((string) ($job['error'] ?? 'see its log'), 0, 250)];
            }
            if (in_array($id, self::ONE_JOB_STEPS, true)) {
                return ['status' => 'ok', 'detail' => 'Done (see the job log).'];
            }
        }
        $this->async = true;
        $this->resumed = $afterJob !== null;
        try {
            [$status, $detail] = $this->call($profile, $opts, $id);
        } catch (SetupJobStarted $e) {
            if ($round >= self::MAX_ROUNDS) {
                return ['status' => 'fail', 'detail' => 'Still not done after ' . self::MAX_ROUNDS . ' jobs: see the job logs, then run the setup again.'];
            }
            return ['status' => 'running', 'detail' => $e->getMessage(), 'job_id' => $e->job];
        } finally {
            $this->async = false;
            $this->resumed = false;
        }
        // Verified after its job: nothing left to do means done.
        if ($afterJob !== null && $status === 'skip') {
            $status = 'ok';
        }
        return ['status' => $status, 'detail' => mb_substr($detail, 0, 300)];
    }

    /** Wizard mode: a step that starts a job returns instead of waiting. */
    private bool $async = false;

    /** Wizard mode: the step runs again after its job succeeded. */
    private bool $resumed = false;

    /** Wizard mode: what the step's earlier jobs of this run already tried (packages). @var list<string> */
    private array $attempted = [];

    /** @param array<string, mixed> $state */
    private function save(array $state): void
    {
        try {
            $this->settings->update(['server_setup' => (string) json_encode($state, JSON_UNESCAPED_SLASHES)], null);
        } catch (ValidationException) {
            // Too long to remember: the results were printed.
        }
    }

    private function say(string $line): void
    {
        ($this->out)($line);
    }

    /** Waits for an agent job, printing its log. Throws when it fails. */
    private function wait(string $job, string $what): void
    {
        $this->jobs->track($job, $what, 'server', (string) $this->server->id(), $this->actorId);
        if ($this->async) {
            throw new SetupJobStarted($job, $what . '…');
        }
        $offset = 0;
        $until = time() + self::JOB_TIMEOUT;
        do {
            usleep($this->pollMicroseconds);
            $res = $this->jobs->poll($job, $offset);
            foreach (preg_split('/\R/', rtrim((string) ($res['log'] ?? ''))) ?: [] as $l) {
                if ($l !== '') {
                    $this->say('   | ' . $l);
                }
            }
            $offset = (int) ($res['offset'] ?? $offset);
        } while (empty($res['done']) && time() < $until);
        if (empty($res['done'])) {
            throw new UserFacingException($what . ': still running after an hour (job ' . $job . ').');
        }
        if (($res['job']['status'] ?? '') !== 'succeeded') {
            throw new UserFacingException($what . ' failed: ' . ($res['job']['error'] ?? 'see the log above'));
        }
    }

    /** @return array<string, array<string, mixed>> components.status by id */
    private function components(): array
    {
        $out = [];
        foreach ((array) json_decode((string) json_encode($this->agent->call('components.status', [], 60)), true) as $c) {
            $out[(string) $c['id']] = $c;
        }
        return $out;
    }

    /** @param array<string, mixed> $c @return list<string> the items of a step left to do */
    private static function todo(array $c, string $step): array
    {
        foreach ((array) ($c['steps'] ?? []) as $s) {
            if (($s['id'] ?? '') === $step && ($s['status'] ?? '') === 'todo') {
                return array_map('strval', (array) ($s['items'] ?? []));
            }
        }
        return [];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepPackages(string $profile, array $opts): array
    {
        $missing = [];
        foreach ($this->components() as $id => $c) {
            if ($id !== 'php' && in_array($id, self::PROFILES[$profile], true)) {
                $missing = array_merge($missing, self::todo($c, 'installed'));
            }
        }
        $missing = array_values(array_unique($missing));
        if ($missing === []) {
            return ['skip', 'Every component of the profile is installed.'];
        }
        // The wizard installs a batch per job: what its earlier jobs
        // installed without success is reported, not tried again.
        $left = array_values(array_diff($missing, $this->attempted));
        if ($left === []) {
            return ['fail', 'Still not installed after their job: ' . implode(', ', $missing) . '. See the job log, or install them under Server → Packages.'];
        }
        $missing = $left;
        $this->say('   Installing: ' . implode(', ', $missing));
        foreach (array_chunk($missing, 10) as $chunk) {
            $this->attempted = array_values(array_unique(array_merge($this->attempted, $chunk)));
            $job = (string) ((array) $this->agent->call('packages.install', ['names' => $chunk]))['job_id'];
            $this->wait($job, 'Install ' . implode(', ', $chunk));
        }
        return ['ok', 'Installed ' . implode(', ', $missing) . '.'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepPhpRepository(string $profile, array $opts): array
    {
        $available = array_map('strval', (array) $this->agent->call('php.available'));
        $missing = array_values(array_diff($opts['php'], $available, $this->installedPhp()));
        if ($missing === []) {
            return ['skip', 'PHP ' . implode(', ', $opts['php']) . ' come from the configured repositories.'];
        }
        if (!$opts['third_party_repos']) {
            return ['skip', 'PHP ' . implode(', ', $missing) . ' is not offered by this system\'s repositories, and third-party repositories are not allowed (--no-third-party-repos): only the offered versions are installed.'];
        }
        $repo = (array) $this->agent->call('php.repository');
        if (!empty($repo['enabled'])) {
            return ['skip', ($repo['name'] ?? 'The PHP repository') . ' is enabled; PHP ' . implode(', ', $missing) . ' is not offered by it.'];
        }
        if (empty($repo['supported'])) {
            return ['skip', 'No PHP repository is supported on this system: PHP ' . implode(', ', $missing) . ' cannot be installed.'];
        }
        $this->say('   Adding ' . ($repo['name'] ?? 'the PHP repository') . ' for PHP ' . implode(', ', $missing) . ' (third-party repository; see the installation guide).');
        $job = (string) ((array) $this->agent->call('php.repository.enable'))['job_id'];
        $this->wait($job, 'Enable the PHP repository');
        return ['ok', ($repo['name'] ?? 'The PHP repository') . ' added.'];
    }

    /** @return list<string> PHP versions with PHP-FPM */
    private function installedPhp(): array
    {
        $out = [];
        foreach ((array) (((array) $this->agent->call('discovery.php'))['versions'] ?? []) as $v) {
            if (!empty($v['fpm_binary'])) {
                $out[] = (string) $v['version'];
            }
        }
        return $out;
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepPhp(string $profile, array $opts): array
    {
        $installed = $this->installedPhp();
        $available = array_map('strval', (array) $this->agent->call('php.available'));
        $want = array_values(array_intersect($opts['php'], array_merge($available, $installed)));
        if ($want === []) {
            return ['fail', 'None of PHP ' . implode(', ', $opts['php']) . ' is offered by the repositories.'];
        }
        // Nothing is removed: versions and extensions in use stay.
        $versions = array_values(array_unique(array_merge($installed, $want)));
        usort($versions, 'version_compare');
        $catalog = (array) $this->agent->call('php.catalog');
        $exts = array_map('strval', (array) ($catalog['defaults'] ?? []));
        foreach ($installed as $v) {
            foreach ((array) $this->agent->call('php.extensions', ['version' => $v]) as $e) {
                if (!empty($e['installed']) && !empty($e['enabled'])) {
                    $exts[] = (string) $e['name'];
                }
            }
        }
        $exts = array_values(array_unique($exts));
        $profileData = ['versions' => $versions, 'extensions' => $exts];
        $plan = (array) $this->agent->call('php.profile_plan', $profileData, 120);
        if (!empty($plan['changes']) && $this->resumed) {
            // Applied, yet still different: applying again would not help.
            return ['fail', 'Applied, but still to do: ' . self::pending($plan) . '. See the job log; the PHP Manager shows the same review.'];
        }
        if (empty($plan['changes'])) {
            $this->rememberPhp($profileData, null);
            return ['skip', 'PHP ' . implode(', ', $versions) . ' with the default extensions are installed.'];
        }
        $res = (array) $this->agent->call('php.profile_apply', $profileData + ['confirm_removals' => false], 120);
        $job = (string) $res['job_id'];
        $this->wait($job, 'Apply PHP profile');
        $this->rememberPhp($profileData, $job);
        $skipped = array_diff($opts['php'], $want);
        return ['ok', 'PHP ' . implode(', ', $versions) . ' with ' . count($exts) . ' extensions.' . ($skipped !== [] ? ' Not offered: PHP ' . implode(', ', $skipped) . '.' : '')];
    }

    /** What a PHP plan still changes, briefly. @param array<string, mixed> $plan */
    private static function pending(array $plan): string
    {
        $out = [];
        foreach ((array) ($plan['items'] ?? []) as $it) {
            if (!in_array($it['action'] ?? '', ['unchanged', 'blocked'], true)) {
                $out[] = 'PHP ' . $it['version'] . (($it['extension'] ?? '') !== '' ? ' ' . $it['extension'] : '') . ' (' . $it['action'] . (($it['detail'] ?? '') !== '' ? ': ' . $it['detail'] : '') . ')';
            }
        }
        $more = count($out) > 6 ? ' and ' . (count($out) - 6) . ' more' : '';
        return implode(', ', array_slice($out, 0, 6)) . $more;
    }

    /** @param array{versions: list<string>, extensions: list<string>} $p */
    private function rememberPhp(array $p, ?string $job): void
    {
        if ($this->settings->string('php_profile') !== '' && $job === null) {
            return;
        }
        try {
            $this->settings->update(['php_profile' => (string) json_encode($p + ($job !== null ? ['job_id' => $job] : []) + ['applied_at' => gmdate('Y-m-d H:i')])], null);
        } catch (ValidationException) {
        }
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepMail(string $profile, array $opts): array
    {
        $c = $this->components()['mail'] ?? [];
        if (empty($c['installed'])) {
            return ['fail', 'The mail packages are not installed (see the first step).'];
        }
        if (!self::stepTodo($c, 'setup')) {
            return ['skip', 'Email is set up already (its settings are left as they are).'];
        }
        $hostname = $opts['hostname'] !== '' ? $opts['hostname'] : strtolower((string) gethostname());
        if (!str_contains($hostname, '.') || str_ends_with($hostname, '.localdomain') || str_ends_with($hostname, '.local')) {
            return ['fail', 'The server has no full host name (' . $hostname . '). Run again with --hostname=server.example.com, or set up email on the Email page.'];
        }
        // Choosing a profile with email is the consent to NimVor editing
        // Postfix's and Dovecot's configuration (backups are kept).
        $this->agent->call('mail.configure', ['hostname' => $hostname, 'confirm_edit_package_config' => true], 300);
        return ['ok', 'Postfix, Dovecot and OpenDKIM set up for ' . $hostname . '.'];
    }

    /** @param array<string, mixed> $c */
    private static function stepTodo(array $c, string $step): bool
    {
        foreach ((array) ($c['steps'] ?? []) as $s) {
            if (($s['id'] ?? '') === $step) {
                return ($s['status'] ?? '') === 'todo';
            }
        }
        return false;
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepDns(string $profile, array $opts): array
    {
        $c = $this->components()['dns'] ?? [];
        if (empty($c['installed'])) {
            return ['fail', 'BIND is not installed (see the first step).'];
        }
        if (!self::stepTodo($c, 'setup')) {
            return ['skip', 'DNS hosting is on already.'];
        }
        // Authoritative only: NimVor's configuration answers for the hosted zones, without recursion.
        $this->agent->call('dns.configure', ['confirm_edit_package_config' => true], 120);
        return ['ok', 'BIND answers for the zones hosted here (authoritative, no recursion).'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepFtp(string $profile, array $opts): array
    {
        $c = $this->components()['ftp'] ?? [];
        if (empty($c['installed'])) {
            return ['fail', 'pure-ftpd is not installed (see the first step).'];
        }
        if (!self::stepTodo($c, 'setup')) {
            return ['skip', 'The FTP server is set up already.'];
        }
        // The defaults: TLS required, passive ports 30000-30049. Another
        // running FTP server is never replaced here, except the pure-ftpd
        // package's own default service, which installing the package just
        // started (the agent replaces it only while it has no accounts).
        $this->agent->call('ftp.configure', ['replace_unused_stock' => true], 120);
        return ['ok', 'FTP server set up (TLS required; passive ports 30000-30049).'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepCaches(string $profile, array $opts): array
    {
        $c = $this->components()['redis'] ?? [];
        if (empty($c['installed'])) {
            return ['fail', 'Redis is not installed (see the first step).'];
        }
        $st = (array) $this->agent->call('redis.info');
        $max = (string) ($st['maxmemory'] ?? '');
        if ($max === '') {
            return ['skip', 'Redis is not answering yet; set its memory limit on the Redis page.'];
        }
        if ($max !== '0') {
            return ['skip', 'Redis has a memory limit already.'];
        }
        // Unlimited Redis memory can push websites out of memory.
        $this->agent->call('redis.configure', ['maxmemory_mb' => 256, 'policy' => 'allkeys-lru']);
        return ['ok', 'Redis keeps at most 256 MB (least recently used keys are evicted).'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepServices(string $profile, array $opts): array
    {
        $done = [];
        foreach ($this->components() as $id => $c) {
            if ($id === 'firewall' || !in_array($id, self::PROFILES[$profile], true)) {
                continue;
            }
            foreach (self::todo($c, 'running') as $unit) {
                $this->agent->call('services.control', ['unit' => $unit, 'op' => 'start'], 90);
                $done[] = 'started ' . $unit;
            }
            foreach (self::todo($c, 'boot') as $unit) {
                $this->agent->call('services.control', ['unit' => $unit, 'op' => 'enable'], 60);
                $done[] = $unit . ' at boot';
            }
        }
        return $done === [] ? ['skip', 'Every service runs and starts at boot.'] : ['ok', ucfirst(implode(', ', $done)) . '.'];
    }

    /**
     * Keeps a guarded firewall change once every critical port (SSH, the
     * panel, the agent's listener) is open; otherwise it is undone now.
     *
     * @param array<string, mixed> $res the action's result
     */
    private function keep(array $res, string $what): void
    {
        $pending = (array) ($res['pending'] ?? []);
        if (empty($res['changed']) || empty($pending['id'])) {
            return;
        }
        $fw = (array) $this->agent->call('firewall.status', [], 60);
        $closed = [];
        // With the firewall off nothing is blocked.
        foreach (!empty($fw['active']) ? (array) ($fw['required'] ?? []) : [] as $r) {
            if (!empty($r['critical']) && empty($r['open'])) {
                $closed[] = ($r['service'] ?? '') . ' ' . $r['port'] . '/' . $r['proto'];
            }
        }
        if ($closed !== []) {
            $this->agent->call('security.undo', ['id' => (string) $pending['id']], 90);
            throw new UserFacingException($what . ': undone, because ' . implode(', ', $closed) . ' would not be reachable. Allow it on the Firewall page and run the setup again.');
        }
        $this->agent->call('security.confirm', ['id' => (string) $pending['id']]);
        $this->audit->record('security.confirm', 'server', 'security', AuditLogger::SUCCESS, ['what' => $what, 'via' => 'setup']);
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepFirewall(string $profile, array $opts): array
    {
        $fw = (array) $this->agent->call('firewall.status', [], 60);
        if (empty($fw['installed'])) {
            return ['fail', 'No firewall is installed (UFW, or firewalld on the RHEL family). Install it and run the setup again.'];
        }
        if (!empty($fw['active'])) {
            return ['skip', 'The firewall is on.'];
        }
        // Enabling opens SSH, the panel and the agent's listener first, and
        // the ports of services listening now.
        $res = (array) $this->agent->call('firewall.set_enabled', ['enabled' => true, 'allow_services' => true], 120);
        $this->keep($res, 'Turning on the firewall');
        $this->audit->record('firewall.enable', 'server', 'security', AuditLogger::SUCCESS, ['via' => 'setup']);
        if (empty(((array) $this->agent->call('firewall.status', [], 60))['active'])) {
            return ['fail', 'The firewall was turned on but reads as off: see the Firewall page.'];
        }
        return ['ok', 'The firewall is on; SSH, the panel and the listening services are allowed.'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepPorts(string $profile, array $opts): array
    {
        $opened = [];
        foreach ($this->components() as $id => $c) {
            if (!in_array($id, self::PROFILES[$profile], true) || ($items = self::todo($c, 'ports')) === []) {
                continue;
            }
            $ports = array_map(static function (string $p): array {
                [$port, $proto] = explode('/', $p, 2) + [1 => 'tcp'];
                return ['port' => $port, 'proto' => $proto];
            }, $items);
            $comment = substr(trim((string) preg_replace(['/[^A-Za-z0-9 ._:\/-]+/', '/ {2,}/'], ' ', (string) $c['name'])), 0, 60);
            $res = (array) $this->agent->call('firewall.allow_ports', ['ports' => $ports, 'comment' => $comment], 90);
            $this->keep($res, 'Opening the ports of ' . $c['name']);
            $this->audit->record('firewall.allow_ports', 'server', 'security', AuditLogger::SUCCESS, ['ports' => $items, 'via' => 'setup']);
            $opened[] = $c['name'] . ' (' . implode(', ', $items) . ')';
        }
        return $opened === [] ? ['skip', 'The components\' ports are open (or the firewall is off).'] : ['ok', 'Opened: ' . implode('; ', $opened) . '.'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepProtection(string $profile, array $opts): array
    {
        $st = (array) $this->agent->call('firewall.protection', [], 60);
        $cfg = (array) ($st['config'] ?? []);
        if (!empty($cfg['enabled'])) {
            return ['skip', 'The firewall protections are on (left as configured).'];
        }
        // Conservative: invalid packets and port-scan flag combinations,
        // and a generous limit of new connections per address.
        $res = (array) $this->agent->call('firewall.protection_set', ['enabled' => true, 'invalid' => true, 'syn_rate' => 100, 'conn_limits' => [], 'port_flood' => [], 'icmp_rate' => 0, 'exempt' => []], 120);
        $this->keep($res, 'The firewall protections');
        return ['ok', 'Invalid packets are dropped; one address may open 100 new connections per second.'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepFail2ban(string $profile, array $opts): array
    {
        $st = (array) $this->agent->call('fail2ban.status', [], 60);
        if (empty($st['installed'])) {
            return ['fail', 'fail2ban is not installed (see the first step).'];
        }
        if (!empty($st['managed_config'])) {
            return ['skip', 'NimVor\'s fail2ban jails are configured (left as they are).'];
        }
        $jails = SecurityDefaultsService::JAILS;
        $this->agent->call('fail2ban.configure', ['jails' => $jails, 'ban_minutes' => 60, 'find_minutes' => 10, 'max_retry' => 5, 'ignore' => []], 120);
        return ['ok', 'Jails ' . implode(', ', $jails) . ': 5 failures in 10 minutes ban for an hour.'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepHardening(string $profile, array $opts): array
    {
        $done = [];
        $k = (array) $this->agent->call('security.fix_kernel', [], 60);
        $done[] = ($k['changed'] ?? []) === [] ? 'kernel settings already recommended' : 'kernel settings: ' . implode(', ', array_map('strval', (array) $k['changed']));
        if ($profile === 'full') {
            // Only the mail server (and root, the panel) may reach remote
            // SMTP ports: websites send through it, so spam from a hacked
            // website is visible in the mail queue and logs.
            $st = (array) $this->agent->call('hardening.status', [], 60);
            if (empty(((array) ($st['smtp'] ?? []))['enabled'])) {
                $this->agent->call('hardening.smtp', ['enabled' => true], 60);
                $done[] = 'SMTP restrictions on';
            } else {
                $done[] = 'SMTP restrictions already on';
            }
        }
        return ['ok', ucfirst(implode('; ', $done)) . '.'];
    }

    /**
     * The recommended security settings (malware scans, fail2ban jails,
     * compilers, SMTP restrictions, metadata block, cPHulk, fork bomb
     * protection, traceroute), turned on once: a later run leaves what an
     * administrator changed alone. They stay switchable on the Security
     * overview.
     *
     * @param array<string, mixed> $opts
     * @return array{0: string, 1: string}
     */
    private function stepSecurityDefaults(string $profile, array $opts): array
    {
        if ($this->settings->bool('security_defaults_applied')) {
            return ['skip', 'The recommended security settings were applied before (left as they are; change them on the Security overview).'];
        }
        $this->securityDefaults->adminIp = $this->actorIp;
        [$status, $detail] = $this->securityDefaults->applyDefaults($this->actorId);
        if ($status !== 'fail') {
            $this->settings->update(['security_defaults_applied' => '1'], null);
        }
        return [$status, $detail];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepAutoUpdates(string $profile, array $opts): array
    {
        $res = (array) $this->agent->call('security.auto_updates', ['enabled' => true], 60);
        if (!empty($res['job_id'])) {
            $this->wait((string) $res['job_id'], 'Automatic security updates');
        }
        return ['ok', 'Security updates are installed automatically (no automatic reboot).'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepPlans(string $profile, array $opts): array
    {
        $made = [];
        $lists = [];
        foreach (self::FEATURE_LISTS as $name => [$desc, $on]) {
            $id = $this->db->scalar('SELECT id FROM feature_lists WHERE name = ?', [$name]);
            if ($id === null) {
                $id = $this->features->save(null, ['name' => $name, 'description' => $desc, 'features' => $on], 0);
                $this->audit->record('feature_list.save', 'feature_list', (string) $id, AuditLogger::SUCCESS, ['name' => $name, 'via' => 'setup']);
                $made[] = 'feature list ' . $name;
            }
            $lists[$name] = (int) $id;
        }
        $actor = ['id' => 0, 'role_scope' => 'all'];
        foreach (self::PLANS as $name => [$desc, $list, $limits]) {
            if ($this->db->scalar('SELECT id FROM plans WHERE owner_id IS NULL AND name = ?', [$name]) !== null) {
                continue;
            }
            $this->plans->save($actor, null, ['name' => $name, 'description' => $desc, 'feature_list_id' => (string) $lists[$list]] + $limits);
            $made[] = 'plan ' . $name;
        }
        return $made === [] ? ['skip', 'The plans and feature lists exist (left as they are).'] : ['ok', 'Created ' . implode(', ', $made) . '.'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepBackups(string $profile, array $opts): array
    {
        $made = [];
        $existing = $this->scheduler->list(null);
        if (array_filter($existing, static fn (array $s): bool => (int) ($s['all_sites'] ?? 0) === 1) === []) {
            // Sunday 02:00, four weeks kept, on this server (a remote
            // storage can be added to the schedule later).
            $this->scheduler->create(null, ['scope' => 'all_sites', 'frequency' => 'weekly', 'weekday' => 0, 'hour' => 2, 'retention' => 4, 'include_files' => true, 'include_databases' => true, 'encrypt' => true]);
            $made[] = 'every website, Sundays at 02:00, 4 kept per website';
        }
        if (array_filter($existing, static fn (array $s): bool => $s['kind'] === 'server') === []) {
            $this->scheduler->create(null, ['frequency' => 'weekly', 'weekday' => 0, 'hour' => 1, 'retention' => 4]);
            $made[] = 'server configuration, Sundays at 01:00, 4 kept';
        }
        return $made === [] ? ['skip', 'Backup schedules exist (left as they are).'] : ['ok', 'Weekly backups: ' . implode('; ', $made) . '. Backups are kept on this server: add a remote storage under Backups for copies elsewhere.'];
    }

    /** @param array<string, mixed> $opts @return array{0: string, 1: string} */
    private function stepProfile(string $profile, array $opts): array
    {
        $this->settings->update(['components_expected' => implode(',', self::PROFILES[$profile])], null);
        return ['ok', self::PROFILE_NAMES[$profile] . ': Server → Components checks ' . implode(', ', self::PROFILES[$profile]) . '.'];
    }
}

