<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\CronService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;

/**
 * REST API v1: a website's cron jobs. Every endpoint calls CronService, as the web interface does: the
 * schedule and command are validated, the server's cron file is rewritten and checked, and the change
 * is undone when the server refuses it. Jobs run as the website's system account.
 */
final class CronApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly CronService $cron,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
    }

    /**
     * @param array<string, mixed> $j
     * @return array<string, mixed>
     */
    private static function jobOut(array $j): array
    {
        return [
            'id' => (int) $j['id'], 'schedule' => $j['schedule'], 'command' => $j['command'], 'description' => (string) $j['description'],
            'enabled' => (bool) $j['enabled'], 'email_output' => (string) ($j['email'] ?? '') !== '',
            'created_at' => $j['created_at'], 'updated_at' => $j['updated_at'],
        ];
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    public function listJobs(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok(array_map([self::class, 'jobOut'], $this->cron->jobs($s)));
    }

    public function showJob(Request $r, string $id, string $job): Response
    {
        $s = $this->site($r, $id);
        $j = $s === null ? null : $this->cron->find($s, (int) $job);
        return $j === null ? self::notFound() : self::ok(self::jobOut($j));
    }

    public function createJob(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $before = array_column($this->cron->jobs($s), 'id');
            $this->cron->save($s, null, self::body($r), (string) ($this->user($r)['email'] ?? ''));
            $new = array_values(array_filter($this->cron->jobs($s), static fn (array $j): bool => !in_array($j['id'], $before, true)));
            return self::ok(self::jobOut((array) $new[0]), 201);
        });
    }

    public function updateJob(Request $r, string $id, string $job): Response
    {
        $s = $this->site($r, $id);
        $j = $s === null ? null : $this->cron->find($s, (int) $job);
        if ($s === null || $j === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $j): Response {
            $in = self::body($r);
            // A PATCH changes only what it names.
            $in += ['schedule' => $j['schedule'], 'command' => $j['command'], 'description' => $j['description'], 'enabled' => (bool) $j['enabled'], 'email_output' => (string) ($j['email'] ?? '') !== ''];
            $this->cron->save($s, (int) $j['id'], $in, (string) ($this->user($r)['email'] ?? ''));
            return self::ok(self::jobOut((array) $this->cron->find($s, (int) $j['id'])));
        });
    }

    public function deleteJob(Request $r, string $id, string $job): Response
    {
        $s = $this->site($r, $id);
        $j = $s === null ? null : $this->cron->find($s, (int) $job);
        if ($s === null || $j === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s, $j): Response {
            $this->cron->delete($s, (int) $j['id']);
            return self::ok(['deleted' => (int) $j['id']]);
        });
    }

    public function runJob(Request $r, string $id, string $job): Response
    {
        $s = $this->site($r, $id);
        $j = $s === null ? null : $this->cron->find($s, (int) $job);
        if ($s === null || $j === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $j): Response {
            $jobId = $this->cron->run($s, $j);
            $this->jobs->track($jobId, 'Cron: ' . mb_substr((string) $j['command'], 0, 60), 'website', (string) $s['id'], (int) $this->user($r)['id']);
            return self::ok(['job_id' => $jobId], 202);
        });
    }

    public function jobRuns(Request $r, string $id, string $job): Response
    {
        $s = $this->site($r, $id);
        $j = $s === null ? null : $this->cron->find($s, (int) $job);
        if ($s === null || $j === null) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok($this->cron->runs($s, $j)));
    }
}
