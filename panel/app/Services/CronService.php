<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Database\Connection;
use NimVor\Support\ValidationException;

/**
 * Website cron jobs. The panel stores the jobs; after every change the
 * complete schedule of the website is sent to the agent (cron.apply),
 * which owns one /etc/cron.d file per website. Jobs always run as the
 * website's system account.
 */
final class CronService
{
    /** Common schedules offered in the form. */
    public const PRESETS = [
        '* * * * *' => 'Every minute',
        '*/5 * * * *' => 'Every 5 minutes',
        '*/15 * * * *' => 'Every 15 minutes',
        '0 * * * *' => 'Every hour',
        '0 3 * * *' => 'Every day at 03:00',
        '0 3 * * 0' => 'Every Sunday at 03:00',
        '0 3 1 * *' => 'First day of the month at 03:00',
    ];

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ?PlanLimits $limits = null,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return list<array<string, mixed>>
     */
    public function jobs(array $site): array
    {
        return array_map(static function (array $r): array {
            $r['enabled'] = (int) $r['enabled'] === 1;
            return $r;
        }, $this->db->fetchAll('SELECT * FROM cron_jobs WHERE website_id = ? ORDER BY id', [(int) $site['id']]));
    }

    /**
     * @param array<string, mixed> $site
     * @return array<string, mixed>|null
     */
    public function find(array $site, int $id): ?array
    {
        foreach ($this->jobs($site) as $j) {
            if ((int) $j['id'] === $id) {
                return $j;
            }
        }
        return null;
    }

    /**
     * Validate cron input; returns normalised values.
     *
     * @param array<string, mixed> $input
     * @return array{schedule: string, command: string, description: string, enabled: bool, email_output: bool}
     */
    public static function validate(array $input): array
    {
        $errors = [];
        $schedule = preg_replace('/\s+/', ' ', trim((string) ($input['schedule'] ?? ''))) ?? '';
        if ($schedule === 'custom' || $schedule === '') {
            $schedule = preg_replace('/\s+/', ' ', trim((string) ($input['schedule_custom'] ?? ''))) ?? '';
        }
        if (!self::validSchedule($schedule)) {
            $errors['schedule'] = 'Use five fields (minute hour day month weekday), e.g. */5 * * * *, or @hourly/@daily/@weekly/@monthly. Names like mon or jan are not supported.';
        }
        $command = trim((string) ($input['command'] ?? ''));
        if ($command === '' || strlen($command) > 2000 || preg_match('/[\x00-\x08\x0a-\x1f\x7f]/', $command)) {
            $errors['command'] = 'Enter the command on a single line (up to 2000 characters).';
        }
        $description = trim((string) ($input['description'] ?? ''));
        if (mb_strlen($description) > 200) {
            $errors['description'] = 'Keep the description under 200 characters.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['schedule' => $schedule, 'command' => $command, 'description' => $description, 'enabled' => filter_var($input['enabled'] ?? true, FILTER_VALIDATE_BOOL), 'email_output' => filter_var($input['email_output'] ?? false, FILTER_VALIDATE_BOOL)];
    }

    /** Addresses the agent accepts for a job's MAILTO line. */
    public static function mailable(string $email): bool
    {
        return strlen($email) <= 254 && preg_match('/^[A-Za-z0-9._+-]{1,64}@[A-Za-z0-9-]{1,63}(\.[A-Za-z0-9-]{1,63})+$/', $email) === 1;
    }

    /**
     * The end of a job's log, split into runs (newest first). Each run is
     * framed by "### nimvor-run start …" and "### nimvor-run end … exit=N";
     * output from before the framing is kept as one earlier entry.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $job
     * @return array{runs: list<array{start: ?string, end: ?string, exit: ?int, manual: bool, output: string}>, truncated: bool, size: int}
     */
    public function runs(array $site, array $job): array
    {
        $res = (array) $this->agent->call('cron.log', ['domain' => $site['domain'], 'root' => $site['root'], 'id' => (int) $job['id']]);
        return ['runs' => self::parseRuns((string) ($res['content'] ?? '')), 'truncated' => !empty($res['truncated']), 'size' => (int) ($res['size'] ?? 0)];
    }

    /** @return list<array{start: ?string, end: ?string, exit: ?int, manual: bool, output: string}> */
    public static function parseRuns(string $log, int $max = 200): array
    {
        $runs = [];
        $cur = null;
        $loose = '';
        foreach (preg_split('/\r?\n/', $log) ?: [] as $line) {
            if (preg_match('/^### nimvor-run start (\S+)( manual)?$/', $line, $m)) {
                if ($cur !== null) {
                    $runs[] = $cur;
                }
                $cur = ['start' => $m[1], 'end' => null, 'exit' => null, 'manual' => isset($m[2]), 'output' => ''];
            } elseif (preg_match('/^### nimvor-run end (\S+) exit=(\d+)$/', $line, $m) && $cur !== null) {
                $cur['end'] = $m[1];
                $cur['exit'] = (int) $m[2];
                $runs[] = $cur;
                $cur = null;
            } elseif ($cur !== null) {
                $cur['output'] .= $line . "\n";
            } elseif ($line !== '') {
                $loose .= $line . "\n";
            }
        }
        if ($cur !== null) {
            $runs[] = $cur;
        }
        if ($loose !== '') {
            array_unshift($runs, ['start' => null, 'end' => null, 'exit' => null, 'manual' => false, 'output' => $loose]);
        }
        foreach ($runs as &$r) {
            $r['output'] = rtrim($r['output'], "\n");
        }
        unset($r);
        return array_slice(array_reverse($runs), 0, $max);
    }

    public static function validSchedule(string $s): bool
    {
        if (in_array($s, ['@hourly', '@daily', '@midnight', '@weekly', '@monthly', '@yearly', '@annually', '@reboot'], true)) {
            return true;
        }
        $f = explode(' ', $s);
        if (count($f) !== 5) {
            return false;
        }
        $limits = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 7]];
        foreach ($f as $i => $field) {
            if (!preg_match('~^(\*|[0-9]{1,2}(-[0-9]{1,2})?)(/[0-9]{1,2})?(,(\*|[0-9]{1,2}(-[0-9]{1,2})?)(/[0-9]{1,2})?)*$~', $field)) {
                return false;
            }
            foreach (explode(',', $field) as $item) {
                preg_match_all('/[0-9]+/', explode('/', $item)[0], $m);
                foreach ($m[0] as $n) {
                    if ((int) $n < $limits[$i][0] || (int) $n > $limits[$i][1]) {
                        return false;
                    }
                }
            }
        }
        return true;
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $input
     * @param string $accountEmail the saving user's email: receives the output when mailing is turned on
     */
    public function save(array $site, ?int $id, array $input, string $accountEmail = ''): void
    {
        $v = self::validate($input);
        $email = '';
        $current = $id === null ? null : $this->find($site, $id);
        if ($v['email_output'] && $current !== null && self::mailable((string) ($current['email'] ?? ''))) {
            $email = (string) $current['email']; // unchanged: keep who receives it
        } elseif ($v['email_output']) {
            if (!self::mailable($accountEmail)) {
                throw new ValidationException(['email_output' => 'Your account has no usable email address for the output.']);
            }
            $email = $accountEmail;
        }
        $now = gmdate('c');
        $before = $this->jobs($site);
        if ($id === null) {
            if (count($before) >= 200) {
                throw new ValidationException(['command' => 'A website can have at most 200 cron jobs.']);
            }
            $this->limits?->assertCanCreate('cron_jobs', $site['owner_id'] === null ? null : (int) $site['owner_id']);
            $this->db->run(
                'INSERT INTO cron_jobs (website_id, server_id, schedule, command, description, enabled, email, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
                [(int) $site['id'], (int) $site['server_id'], $v['schedule'], $v['command'], $v['description'], $v['enabled'] ? 1 : 0, $email, $now, $now]
            );
        } else {
            $this->db->run(
                'UPDATE cron_jobs SET schedule = ?, command = ?, description = ?, enabled = ?, email = ?, updated_at = ? WHERE id = ? AND website_id = ?',
                [$v['schedule'], $v['command'], $v['description'], $v['enabled'] ? 1 : 0, $email, $now, $id, (int) $site['id']]
            );
        }
        $this->applyOrRevert($site, $before, $id === null ? 'cron.create' : 'cron.update', $v);
    }

    /** @param array<string, mixed> $site */
    public function setEnabled(array $site, int $id, bool $enabled): void
    {
        $before = $this->jobs($site);
        $this->db->run('UPDATE cron_jobs SET enabled = ?, updated_at = ? WHERE id = ? AND website_id = ?', [$enabled ? 1 : 0, gmdate('c'), $id, (int) $site['id']]);
        $this->applyOrRevert($site, $before, $enabled ? 'cron.enable' : 'cron.disable', ['id' => $id]);
    }

    /** @param array<string, mixed> $site */
    public function delete(array $site, int $id): void
    {
        $before = $this->jobs($site);
        $this->db->run('DELETE FROM cron_jobs WHERE id = ? AND website_id = ?', [$id, (int) $site['id']]);
        $this->applyOrRevert($site, $before, 'cron.delete', ['id' => $id]);
    }

    /**
     * Run a job now; returns the agent job id.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $job
     */
    public function run(array $site, array $job): string
    {
        if (!empty($site['account_suspended'])) {
            throw new \NimVor\Support\UserFacingException('The account is suspended; its cron jobs do not run.');
        }
        $res = (array) $this->agent->call('cron.run', ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'id' => (int) $job['id'], 'command' => $job['command']]);
        $this->db->run('UPDATE cron_jobs SET last_run_job = ? WHERE id = ?', [(string) $res['job_id'], (int) $job['id']]);
        $this->audit->record('cron.run', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'job' => (int) $job['id']]);
        return (string) $res['job_id'];
    }

    /** Push the stored schedule to the server (e.g. after re-applying a website). @param array<string, mixed> $site */
    public function apply(array $site): void
    {
        $jobs = array_map(static fn (array $j): array => [
            'id' => (int) $j['id'],
            'schedule' => (string) $j['schedule'],
            'command' => (string) $j['command'],
            // A suspended account's jobs do not run; their setting is kept.
            'enabled' => (bool) $j['enabled'] && empty($site['account_suspended']),
            'email' => self::mailable((string) ($j['email'] ?? '')) ? (string) $j['email'] : '',
        ], $this->jobs($site));
        $this->agent->call('cron.apply', ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'jobs' => $jobs]);
    }

    /** Remove the website's cron file from the server (before deletion). @param array<string, mixed> $site */
    public function removeFromServer(array $site): void
    {
        $this->agent->call('cron.apply', ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'jobs' => []]);
    }

    /**
     * Apply the schedule; on failure put the stored jobs back as they were.
     *
     * @param array<string, mixed> $site
     * @param list<array<string, mixed>> $before
     * @param array<string, mixed> $details
     */
    private function applyOrRevert(array $site, array $before, string $action, array $details): void
    {
        try {
            $this->apply($site);
        } catch (\Throwable $e) {
            $this->db->transaction(function (Connection $db) use ($site, $before): void {
                $db->run('DELETE FROM cron_jobs WHERE website_id = ?', [(int) $site['id']]);
                foreach ($before as $j) {
                    $db->run(
                        'INSERT INTO cron_jobs (id, website_id, server_id, schedule, command, description, enabled, last_run_job, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
                        [$j['id'], $j['website_id'], $j['server_id'], $j['schedule'], $j['command'], $j['description'], $j['enabled'] ? 1 : 0, $j['last_run_job'], $j['created_at'], $j['updated_at']]
                    );
                }
            });
            $this->audit->record($action, 'website', (string) $site['id'], AuditLogger::FAILURE, $details + ['domain' => $site['domain'], 'error' => $e->getMessage()]);
            throw $e;
        }
        $this->audit->record($action, 'website', (string) $site['id'], AuditLogger::SUCCESS, $details + ['domain' => $site['domain']]);
    }
}
