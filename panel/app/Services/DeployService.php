<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Security\Crypto;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Git deployment: a repository per website, cloned and updated by the
 * agent as the website account with a per-site deploy key. Deployments
 * are triggered from the panel or by a signed webhook.
 */
final class DeployService
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly Crypto $crypto,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
        private readonly NotificationService $notifications,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return array<string, mixed>|null
     */
    public function config(array $site): ?array
    {
        $row = $this->db->fetch('SELECT * FROM git_repositories WHERE website_id = ?', [(int) $site['id']]);
        if ($row === null) {
            return null;
        }
        $hooks = json_decode((string) $row['hooks'], true);
        $row['hooks'] = is_array($hooks) ? $hooks : [];
        $row['auto_deploy'] = (int) $row['auto_deploy'] === 1;
        return $row;
    }

    /** @param array<string, mixed> $config */
    public function webhookSecret(array $config): string
    {
        return $config['webhook_secret_enc'] === '' ? '' : $this->crypto->decrypt((string) $config['webhook_secret_enc']);
    }

    /**
     * @param array<string, mixed> $input
     * @return array{repo: string, branch: string, path: string, hooks: list<string>, auto_deploy: bool}
     */
    public static function validate(array $input): array
    {
        $errors = [];
        $repo = trim((string) ($input['repo'] ?? ''));
        if (!preg_match('#^(https://[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~/-]+|git@[A-Za-z0-9.-]+:[A-Za-z0-9._~/-]+|ssh://git@[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~/-]+)$#', $repo) || str_contains($repo, '..')) {
            $errors['repo'] = 'Use an https:// URL or an SSH address like git@github.com:owner/repo.git.';
        }
        $branch = trim((string) ($input['branch'] ?? 'main'));
        if (!preg_match('~^[A-Za-z0-9._/-]{1,100}$~', $branch) || str_starts_with($branch, '-') || str_contains($branch, '..')) {
            $errors['branch'] = 'Enter a valid branch name.';
        }
        $path = trim((string) ($input['path'] ?? ''), " /");
        $first = explode('/', $path)[0];
        if ($path === '' || !preg_match('~^[A-Za-z0-9._/-]{1,200}$~', $path) || preg_match('~(^|/)\.\.?(/|$)~', $path) || in_array($first, ['logs', 'tmp', 'backups', '.ssh'], true)) {
            $errors['path'] = 'Choose a folder inside the website (not logs, tmp, backups or .ssh), e.g. app.';
        }
        $hooks = [];
        foreach (preg_split('/\R/', (string) ($input['hooks'] ?? '')) ?: [] as $line) {
            $line = trim($line);
            if ($line !== '') {
                $hooks[] = $line;
            }
        }
        if (count($hooks) > 20) {
            $errors['hooks'] = 'At most 20 commands.';
        }
        foreach ($hooks as $h) {
            if (strlen($h) > 2000) {
                $errors['hooks'] = 'Each command must be under 2000 characters.';
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['repo' => $repo, 'branch' => $branch, 'path' => $path, 'hooks' => $hooks, 'auto_deploy' => filter_var($input['auto_deploy'] ?? false, FILTER_VALIDATE_BOOL)];
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $input
     */
    public function configure(array $site, array $input): void
    {
        $v = self::validate($input);
        $now = gmdate('c');
        $hooks = json_encode($v['hooks'], JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
        if ($this->config($site) === null) {
            $this->db->run(
                'INSERT INTO git_repositories (website_id, repo, branch, path, hooks, auto_deploy, webhook_token, webhook_secret_enc, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
                [(int) $site['id'], $v['repo'], $v['branch'], $v['path'], $hooks, $v['auto_deploy'] ? 1 : 0, bin2hex(random_bytes(20)), $this->crypto->encrypt(bin2hex(random_bytes(24))), $now, $now]
            );
        } else {
            $this->db->run(
                'UPDATE git_repositories SET repo = ?, branch = ?, path = ?, hooks = ?, auto_deploy = ?, updated_at = ? WHERE website_id = ?',
                [$v['repo'], $v['branch'], $v['path'], $hooks, $v['auto_deploy'] ? 1 : 0, $now, (int) $site['id']]
            );
        }
        $this->audit->record('deploy.configure', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'repo' => $v['repo'], 'branch' => $v['branch'], 'hooks' => count($v['hooks'])]);
    }

    /** @param array<string, mixed> $site */
    public function rotateWebhook(array $site): void
    {
        $this->db->run('UPDATE git_repositories SET webhook_token = ?, webhook_secret_enc = ? WHERE website_id = ?', [bin2hex(random_bytes(20)), $this->crypto->encrypt(bin2hex(random_bytes(24))), (int) $site['id']]);
        // Deliveries signed with the old secret are refused anyway.
        $this->db->run('DELETE FROM webhook_deliveries WHERE website_id = ?', [(int) $site['id']]);
        $this->audit->record('deploy.webhook_rotate', 'website', (string) $site['id']);
    }

    /** @param array<string, mixed> $site */
    public function remove(array $site): void
    {
        $this->db->run('DELETE FROM git_repositories WHERE website_id = ?', [(int) $site['id']]);
        $this->audit->record('deploy.remove', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'note' => 'files were left in place']);
    }

    /** Create (if needed) and return the site's public deploy key. @param array<string, mixed> $site */
    public function publicKey(array $site): string
    {
        $res = (array) $this->agent->call('deploy.key', ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root']], 60);
        $key = (string) ($res['public_key'] ?? '');
        $this->db->run('UPDATE git_repositories SET public_key = ? WHERE website_id = ?', [$key, (int) $site['id']]);
        return $key;
    }

    /**
     * Start a deployment; returns the agent job id.
     *
     * @param array<string, mixed> $site
     */
    public function deploy(array $site, string $trigger, ?int $userId): string
    {
        $cfg = $this->config($site);
        if ($cfg === null) {
            throw new UserFacingException('Connect a Git repository first.');
        }
        if ($this->db->scalar("SELECT COUNT(*) FROM deployments WHERE website_id = ? AND status = 'running' AND created_at > ?", [(int) $site['id'], gmdate('c', time() - 3600)]) > 0) {
            $this->refresh($site);
        }
        try {
            $res = (array) $this->agent->call('deploy.run', [
                'domain' => $site['domain'],
                'user' => $site['system_user'],
                'root' => $site['root'],
                'repo' => $cfg['repo'],
                'branch' => $cfg['branch'],
                'path' => $cfg['path'],
                'hooks' => $cfg['hooks'],
            ]);
        } catch (AgentException $e) {
            $this->db->run('INSERT INTO deployments (website_id, trigger, branch, status, error, user_id, created_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)', [(int) $site['id'], $trigger, $cfg['branch'], 'failed', $e->getMessage(), $userId, gmdate('c'), gmdate('c')]);
            throw $e;
        }
        $job = (string) $res['job_id'];
        $this->db->run('INSERT INTO deployments (website_id, job_id, trigger, branch, status, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)', [(int) $site['id'], $job, $trigger, $cfg['branch'], 'running', $userId, gmdate('c')]);
        $this->audit->record('deploy.run', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'trigger' => $trigger, 'branch' => $cfg['branch'], 'job' => $job]);
        return $job;
    }

    /** Update running deployments of this server's websites (scheduler pass). */
    public function refreshRunning(): void
    {
        $ids = $this->db->fetchAll(
            "SELECT DISTINCT d.website_id FROM deployments d JOIN websites w ON w.id = d.website_id WHERE d.status = 'running' AND w.server_id = ?",
            [$this->server->id()]
        );
        foreach ($ids as $row) {
            $this->refresh(['id' => (int) $row['website_id']]);
        }
    }

    /**
     * Update running deployments from their agent jobs.
     *
     * @param array<string, mixed> $site
     */
    public function refresh(array $site): void
    {
        foreach ($this->db->fetchAll("SELECT * FROM deployments WHERE website_id = ? AND status = 'running'", [(int) $site['id']]) as $d) {
            try {
                $job = (array) $this->agent->call('jobs.get', ['id' => $d['job_id']]);
            } catch (AgentException $e) {
                if ($e->errorCode() === 'not_found') {
                    $this->db->run("UPDATE deployments SET status = 'unknown', finished_at = ? WHERE id = ?", [gmdate('c'), (int) $d['id']]);
                }
                continue;
            }
            $status = (string) ($job['status'] ?? '');
            if ($status !== 'succeeded' && $status !== 'failed') {
                continue;
            }
            $result = (array) ($job['result'] ?? []);
            $this->db->run(
                'UPDATE deployments SET status = ?, commit_hash = ?, message = ?, error = ?, finished_at = ? WHERE id = ?',
                [$status, (string) ($result['commit'] ?? ''), mb_substr((string) ($result['message'] ?? ''), 0, 300), (string) ($job['error'] ?? ''), (string) ($job['finished_at'] ?? gmdate('c')), (int) $d['id']]
            );
        }
    }

    /**
     * @param array<string, mixed> $site
     * @return list<array<string, mixed>>
     */
    public function history(array $site, int $limit = 20): array
    {
        return $this->db->fetchAll('SELECT d.*, u.username FROM deployments d LEFT JOIN users u ON u.id = d.user_id WHERE d.website_id = ? ORDER BY d.id DESC LIMIT ?', [(int) $site['id'], $limit]);
    }

    /**
     * Handle a push webhook. Returns [HTTP status, message].
     *
     * Accepted signatures: GitHub/Gitea/Forgejo (X-Hub-Signature-256,
     * HMAC-SHA256 of the body) and GitLab (X-Gitlab-Token).
     *
     * A delivery starts a deployment once: the hash of its body is recorded
     * before the deployment starts, and the same body is refused afterwards
     * (409). The body is what the signature covers — delivery-ID headers
     * are not signed, so they could be changed in a replay — and no two
     * pushes have the same body (commit IDs, timestamps).
     *
     * @param array<string, string> $headers lower-case names
     * @return array{0: int, 1: string}
     */
    public function webhook(string $token, array $headers, string $body): array
    {
        if (!preg_match('/^[a-f0-9]{40}$/', $token)) {
            return [404, 'Unknown webhook.'];
        }
        $cfg = $this->db->fetch('SELECT g.*, w.server_id FROM git_repositories g JOIN websites w ON w.id = g.website_id WHERE g.webhook_token = ?', [$token]);
        if ($cfg === null) {
            return [404, 'Unknown webhook.'];
        }
        $secret = $this->crypto->decrypt((string) $cfg['webhook_secret_enc']);
        $sig = $headers['x-hub-signature-256'] ?? '';
        $gitlab = $headers['x-gitlab-token'] ?? '';
        $valid = ($sig !== '' && hash_equals('sha256=' . hash_hmac('sha256', $body, $secret), $sig))
            || ($gitlab !== '' && hash_equals($secret, $gitlab));
        if (!$valid) {
            $this->audit->record('deploy.webhook', 'website', (string) $cfg['website_id'], AuditLogger::DENIED, ['reason' => 'bad signature']);
            // At most one notification an hour: every refused request is in the audit log.
            $this->notifications->notify('webhook-signature:' . $cfg['website_id'] . ':' . gmdate('YmdH'), 'webhook', 'warning', 'Deploy webhook refused: invalid signature', [
                'server_id' => (int) $cfg['server_id'], 'website_id' => (int) $cfg['website_id'], 'link' => '/sites/' . (int) $cfg['website_id'] . '/git',
                'body' => 'A request to the deploy webhook had a missing or wrong signature and was refused. If your Git host shows failed deliveries, copy the webhook secret again; if not, someone may be probing the URL (rotate it).',
            ]);
            return [401, 'Invalid signature.'];
        }
        if (($headers['x-github-event'] ?? '') === 'ping') {
            return [200, 'pong'];
        }
        $payload = json_decode($body, true);
        $ref = is_array($payload) ? (string) ($payload['ref'] ?? '') : '';
        if ($ref !== '' && $ref !== 'refs/heads/' . $cfg['branch']) {
            return [202, 'Ignored: push to ' . $ref . ', deploying ' . $cfg['branch'] . '.'];
        }
        if ((int) $cfg['auto_deploy'] !== 1) {
            return [202, 'Ignored: automatic deployment is turned off.'];
        }
        $site = $this->sites->find((int) $cfg['server_id'], (int) $cfg['website_id'], null);
        if ($site === null || (int) $cfg['server_id'] !== $this->server->id()) {
            return [404, 'Unknown webhook.'];
        }
        $hash = hash('sha256', $body);
        $new = $this->db->run(
            'INSERT OR IGNORE INTO webhook_deliveries (website_id, body_sha256, received_at) VALUES (?, ?, ?)',
            [(int) $cfg['website_id'], $hash, gmdate('Y-m-d H:i:s')],
        )->rowCount();
        if ($new === 0) {
            $this->audit->record('deploy.webhook', 'website', (string) $cfg['website_id'], AuditLogger::DENIED, ['reason' => 'replayed delivery']);
            $this->notifications->notify('webhook-replay:' . $cfg['website_id'] . ':' . $hash, 'webhook', 'warning', 'Deploy webhook refused: delivery already processed', [
                'server_id' => (int) $cfg['server_id'], 'website_id' => (int) $cfg['website_id'], 'link' => '/sites/' . (int) $cfg['website_id'] . '/git',
                'body' => 'A delivery that had already started a deployment was sent again and refused. A Git host redelivering it is harmless; otherwise someone may have captured a request (rotate the webhook).',
            ]);
            return [409, 'Ignored: this delivery was already processed. Deploy from the panel to deploy again.'];
        }
        try {
            $job = $this->deploy($site, 'webhook', null);
        } catch (UserFacingException $e) {
            // Nothing was deployed: the Git host may deliver it again.
            $this->db->run('DELETE FROM webhook_deliveries WHERE website_id = ? AND body_sha256 = ?', [(int) $cfg['website_id'], $hash]);
            $this->notifications->notify('webhook-start:' . $cfg['website_id'] . ':' . $hash, 'job', 'error', 'Deploy ' . $site['domain'] . ' could not start', [
                'website_id' => (int) $site['id'], 'link' => '/sites/' . (int) $site['id'] . '/git', 'body' => $e->getMessage(),
            ]);
            return [502, 'Deployment could not start: ' . $e->getMessage()];
        }
        // Followed like deployments started in the panel, so its result is reported.
        $this->jobs->track($job, 'Deploy ' . $site['domain'], 'website', (string) $site['id'], null);
        return [202, 'Deployment started (job ' . $job . ').'];
    }
}
