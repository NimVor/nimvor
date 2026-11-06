<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Agent\AgentHttpResponse;
use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

/**
 * Backups > Remote fetched files: a file is fetched from an SFTP or FTPS
 * server (the server's key confirmed first, the login used once and never
 * kept), listed, and deleted one at a time or in bulk, with its file.
 */
final class FetchedFilesTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('backup.key_info', ['exists' => true, 'key_id' => 'abcd', 'path' => '/var/lib/nimvor-agent/backup.key']);
        $this->agent->succeed('backup.account_files', ['files' => []]);
    }

    /** @return array<string, string> */
    private function form(array $over = []): array
    {
        return $over + [
            'protocol' => 'sftp', 'host' => 'files.example.com', 'port' => '', 'username' => 'bk', 'password' => 'S3cret-pass-1',
            'private_key' => '', 'remote_path' => '/backups/site.tar.gz', 'name' => '', 'host_key' => 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl',
        ];
    }

    private function record(string $status, string $file = '', string $job = 'aaaaaaaaaaaaaaaa'): int
    {
        $this->db->run(
            'INSERT INTO fetched_files (server_id, protocol, host, port, username, remote_path, file_name, size, sha256, status, error, job_id, created_at) VALUES (1, ?, ?, 22, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
            ['sftp', 'files.example.com', 'bk', '/backups/' . ($file ?: 'x.tar'), $file, $status === 'completed' ? 2048 : 0, $status === 'completed' ? str_repeat('ab', 32) : '', $status, $status === 'failed' ? 'curl: (78) No such file' : '', $job, gmdate('c')]
        );
        return $this->db->lastInsertId();
    }

    private function job(string $status, array $result = [], string $error = ''): AgentHttpResponse
    {
        return new AgentHttpResponse(200, (string) json_encode(['success' => true, 'action' => 'jobs.get', 'message' => 'OK', 'data' => ['id' => 'x', 'status' => $status, 'result' => $result, 'error' => $error], 'request_id' => 't']));
    }

    public function testTheTabAndTheWindowAreOfferedToAdministratorsOnly(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/backups')->body();
        foreach (['Remote fetched files', 'data-bs-target="#fetched"', 'id="fetch-remote"', 'Fetch a remote file', 'data-fx-form', 'name="remote_path"', 'name="host"', 'name="port"', 'name="username"', 'name="password"', 'name="private_key"', 'backups-fetch.js', 'No file fetched yet'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        self::assertStringNotContainsString('<script>', $page);
        self::assertStringNotContainsString('<style', $page);

        $this->seedUser('alice', 'customer');
        $this->post('/logout');
        $this->login('alice');
        $page = $this->request('GET', '/backups')->body();
        self::assertStringNotContainsString('Remote fetched files', $page);
        self::assertStringNotContainsString('fetch-remote', $page);
        $before = count($this->agent->requests);
        self::assertSame(403, $this->post('/backups/fetched/probe', $this->form())->status());
        $res = $this->post('/backups/fetched', $this->form());
        self::assertSame('/backups', $res->header('Location'));
        $this->post('/backups/fetched/delete', ['ids' => ['1']]);
        self::assertSame(0, $this->calls('backup.fetch_probe') + $this->calls('backup.fetch_remote') + $this->calls('backup.fetched_delete'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM fetched_files'));
        self::assertGreaterThanOrEqual($before, count($this->agent->requests));
    }

    public function testCheckingTheServerSendsNoLoginAndReturnsTheKeyToConfirm(): void
    {
        $this->login('admin');
        $this->agent->succeed('backup.fetch_probe', ['type' => 'sftp', 'host_key' => 'ssh-ed25519 AAAA', 'key_type' => 'ssh-ed25519', 'fingerprint' => 'SHA256:abc']);
        $res = $this->post('/backups/fetched/probe', $this->form());
        self::assertSame(200, $res->status());
        $body = json_decode($res->body(), true);
        self::assertTrue($body['success']);
        self::assertSame('SHA256:abc', $body['data']['fingerprint']);
        self::assertSame('ssh-ed25519 AAAA', $body['data']['host_key']);
        self::assertSame(22, $body['data']['port']);
        $call = $this->lastCall('backup.fetch_probe');
        self::assertSame(['host', 'port', 'tls', 'type'], $this->sorted(array_keys($call)));
        self::assertStringNotContainsString('S3cret', (string) json_encode($call));
        self::assertStringNotContainsString('S3cret', $res->body());

        // An FTPS server's certificate key is returned instead.
        $this->agent->succeed('backup.fetch_probe', ['type' => 'ftp', 'tls_pin' => 'sha256//' . str_repeat('A', 43) . '=', 'subject' => 'CN=ftp.example.com']);
        $res = $this->post('/backups/fetched/probe', $this->form(['protocol' => 'ftp', 'tls' => 'implicit']));
        $body = json_decode($res->body(), true);
        self::assertSame('CN=ftp.example.com', $body['data']['subject']);
        $call = $this->lastCall('backup.fetch_probe');
        self::assertSame('ftp', $call['type']);
        self::assertSame(990, $call['port']);
        self::assertSame('implicit', $call['tls']);
    }

    public function testAnAddressInTheFileFieldDecidesTheServerAndItsPasswordIsIgnored(): void
    {
        $this->login('admin');
        $this->agent->succeed('backup.fetch_probe', ['type' => 'sftp', 'host_key' => 'ssh-ed25519 AAAA', 'key_type' => 'ssh-ed25519', 'fingerprint' => 'SHA256:abc']);
        $res = $this->post('/backups/fetched/probe', ['protocol' => 'ftp', 'host' => '', 'username' => '', 'remote_path' => 'sftp://backup:hunter2@files.example.com:2222/data/my%20site.tar.gz']);
        $body = json_decode($res->body(), true);
        self::assertSame('sftp', $body['data']['protocol']);
        self::assertSame('files.example.com', $body['data']['host']);
        self::assertSame(2222, $body['data']['port']);
        self::assertSame('backup', $body['data']['username']);
        self::assertSame('/data/my site.tar.gz', $body['data']['remote_path']);
        self::assertStringNotContainsString('hunter2', $res->body());
        self::assertSame('files.example.com', $this->lastCall('backup.fetch_probe')['host']);

        // ftps:// means implicit TLS.
        $this->agent->succeed('backup.fetch_probe', ['type' => 'ftp', 'tls_pin' => 'sha256//' . str_repeat('A', 43) . '=', 'subject' => 'CN=x']);
        $body = json_decode($this->post('/backups/fetched/probe', ['remote_path' => 'ftps://bk@ftp.example.com/in/a.tar'])->body(), true);
        self::assertSame('ftp', $body['data']['protocol']);
        self::assertSame('implicit', $body['data']['tls']);
        self::assertSame(990, $body['data']['port']);
    }

    /** @return list<array{0: array<string, string>, 1: string}> */
    public static function badForms(): array
    {
        return [
            'folder' => [['remote_path' => '/backups/'], 'remote_path'],
            'parent folder' => [['remote_path' => '/backups/../etc/passwd'], 'remote_path'],
            'no path' => [['remote_path' => ''], 'remote_path'],
            'control character' => [['remote_path' => "/a\nb"], 'remote_path'],
            'address without a file' => [['remote_path' => 'sftp://files.example.com/'], 'remote_path'],
            'host with a path' => [['host' => 'evil.example.com/x'], 'host'],
            'host with a space' => [['host' => 'bad host'], 'host'],
            'no host' => [['host' => ''], 'host'],
            'port 0' => [['port' => '0'], 'port'],
            'port too big' => [['port' => '70000'], 'port'],
            'port text' => [['port' => 'ssh'], 'port'],
            'unknown protocol' => [['protocol' => 'http'], 'protocol'],
            'plain ftp' => [['protocol' => 'ftp', 'tls' => 'none'], 'tls'],
            'user with a colon' => [['username' => 'bk:x'], 'username'],
            'no login' => [['password' => '', 'private_key' => ''], 'password'],
            'bad key' => [['password' => '', 'private_key' => 'not a key'], 'private_key'],
            'bad save name' => [['name' => '../x'], 'name'],
            'partial name' => [['name' => 'x.partial'], 'name'],
        ];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('badForms')]
    public function testBadInputIsRefusedWithAFieldErrorAndNothingIsSent(array $over, string $field): void
    {
        $this->login('admin');
        // The server's key is asked for without a login, so only the server and the file are checked then.
        if (!in_array($field, ['username', 'password', 'private_key', 'name'], true)) {
            $res = $this->post('/backups/fetched/probe', $this->form($over));
            self::assertSame(422, $res->status(), $res->body());
            $body = json_decode($res->body(), true);
            self::assertFalse($body['success']);
            self::assertArrayHasKey($field, $body['error']['fields']);
            self::assertSame(0, $this->calls('backup.fetch_probe'));
        }

        $res = $this->post('/backups/fetched', $this->form($over));
        self::assertSame('/backups#fetched', $res->header('Location'));
        self::assertSame(0, $this->calls('backup.fetch_remote'));
        self::assertArrayHasKey($field, (array) $this->flash('errors'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM fetched_files'));
    }

    public function testFetchingNeedsTheServersKeyToHaveBeenConfirmed(): void
    {
        $this->login('admin');
        $res = $this->post('/backups/fetched', $this->form(['host_key' => '']));
        self::assertSame('/backups#fetched', $res->header('Location'));
        self::assertArrayHasKey('host', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('backup.fetch_remote'));
        $res = $this->post('/backups/fetched', $this->form(['protocol' => 'ftp', 'host_key' => 'ssh-ed25519 AAAA', 'tls_pin' => '']));
        self::assertSame(0, $this->calls('backup.fetch_remote'));
    }

    public function testFetchStartsAJobKeepsARecordAndNeverStoresTheLogin(): void
    {
        $this->login('admin');
        $this->agent->succeed('backup.fetch_remote', ['job_id' => 'abcdef0123456789']);
        $res = $this->post('/backups/fetched', $this->form(['private_key' => "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----", 'name' => 'site-copy.tar.gz']));
        self::assertSame('/backups#fetched', $res->header('Location'), (string) $this->flash('error'));
        self::assertStringContainsString('Fetching the file has started', (string) $this->flash('success'));
        self::assertSame('abcdef0123456789', $this->flash('job'));

        $call = $this->lastCall('backup.fetch_remote');
        self::assertSame('/backups/site.tar.gz', $call['path']);
        self::assertSame('site-copy.tar.gz', $call['name']);
        self::assertSame('sftp', $call['remote']['type']);
        self::assertSame('files.example.com', $call['remote']['host']);
        self::assertSame(22, $call['remote']['port']);
        self::assertSame('S3cret-pass-1', $call['remote']['password'], 'the agent gets the login for this transfer');
        self::assertStringStartsWith('ssh-ed25519 ', $call['remote']['host_key']);
        self::assertStringContainsString('PRIVATE KEY', $call['remote']['private_key']);

        $row = $this->db->fetch('SELECT * FROM fetched_files');
        self::assertSame('running', $row['status']);
        self::assertSame('abcdef0123456789', $row['job_id']);
        self::assertSame(['sftp', 'files.example.com', 22, 'bk', '/backups/site.tar.gz'], [$row['protocol'], $row['host'], (int) $row['port'], $row['username'], $row['remote_path']]);

        // Nothing of the login is kept anywhere in the panel's database.
        foreach (['fetched_files', 'audit_logs', 'agent_jobs'] as $table) {
            self::assertStringNotContainsString('S3cret', (string) json_encode($this->db->fetchAll("SELECT * FROM {$table}")), $table);
            self::assertStringNotContainsString('PRIVATE KEY', (string) json_encode($this->db->fetchAll("SELECT * FROM {$table}")), $table);
        }
        $audit = $this->db->fetch("SELECT * FROM audit_logs WHERE action = 'backup.fetch'");
        self::assertSame('success', $audit['result']);
        self::assertStringContainsString('files.example.com', (string) $audit['details']);

        // While it runs the list says so and the row cannot be ticked.
        $this->agent->responses['/v1/actions/jobs.get'] = $this->job('running');
        $page = $this->request('GET', '/backups')->body();
        self::assertStringContainsString('Fetching…', $page);
        self::assertMatchesRegularExpression('~<input[^>]*name="ids\[\]"[^>]*disabled~', $page);
    }

    public function testAFailedValidationFlashesTheFormBackWithoutThePasswordOrTheKey(): void
    {
        $this->login('admin');
        $this->post('/backups/fetched', $this->form(['remote_path' => '/backups/', 'private_key' => 'k']));
        $old = (array) $this->flash('old');
        self::assertSame('files.example.com', $old['host']);
        self::assertArrayNotHasKey('password', $old);
        self::assertArrayNotHasKey('private_key', $old);
        self::assertSame('1', $old['fetch_form']);
        // The window opens again with the errors and without the key confirmation.
        $this->post('/backups/fetched', $this->form(['remote_path' => '/backups/']));
        $page = $this->request('GET', '/backups')->body();
        self::assertStringContainsString('data-nim-modal-open', $page);
    }

    public function testFinishedFetchesAppearAsCompletedOrFailed(): void
    {
        $this->login('admin');
        $ok = $this->record('running', '', 'aaaaaaaaaaaaaaaa');
        $this->agent->responses['/v1/actions/jobs.get'] = $this->job('succeeded', ['name' => 'site.tar.gz', 'size' => 5242880, 'sha256' => str_repeat('cd', 32)]);
        $page = $this->request('GET', '/backups')->body();
        $row = $this->db->fetch('SELECT * FROM fetched_files WHERE id = ?', [$ok]);
        self::assertSame(['completed', 'site.tar.gz', 5242880, str_repeat('cd', 32)], [$row['status'], $row['file_name'], (int) $row['size'], $row['sha256']]);
        self::assertStringContainsString('Completed', $page);
        self::assertStringContainsString('site.tar.gz', $page);
        self::assertStringContainsString('5 MB', str_replace(['MiB', '5.0 MB'], ['MB', '5 MB'], $page));
        self::assertStringContainsString(str_repeat('cd', 32), $page);

        $bad = $this->record('running', '', 'bbbbbbbbbbbbbbbb');
        $this->agent->responses['/v1/actions/jobs.get'] = $this->job('failed', [], 'the file could not be fetched: curl: (78) No such file');
        $page = $this->request('GET', '/backups')->body();
        $row = $this->db->fetch('SELECT * FROM fetched_files WHERE id = ?', [$bad]);
        self::assertSame('failed', $row['status']);
        self::assertStringContainsString('No such file', $page);
        self::assertStringContainsString('Failed', $page);
    }

    public function testAJobTheAgentForgotMakesTheRecordFailedNotStuck(): void
    {
        $this->login('admin');
        $id = $this->record('running');
        $this->agent->fail('jobs.get', 404, 'not_found', 'no such job');
        $this->request('GET', '/backups');
        self::assertSame('failed', $this->db->scalar('SELECT status FROM fetched_files WHERE id = ?', [$id]));
    }

    public function testDeletingOneAsksFirstThenRemovesTheRecordAndTheFile(): void
    {
        $this->login('admin');
        $id = $this->record('completed', 'site.tar.gz');
        $page = $this->request('GET', '/backups')->body();
        // The confirmation window is the page's shared one: the form carries the question.
        self::assertMatchesRegularExpression('~<form[^>]*data-nim-confirm="Delete the fetched file site\.tar\.gz\?[^"]*"[^>]*>.*?name="ids\[\]" value="' . $id . '"~s', $page);

        $this->agent->succeed('backup.fetched_delete', ['results' => [['name' => 'site.tar.gz', 'deleted' => true]]]);
        $res = $this->post('/backups/fetched/delete', ['ids' => [(string) $id]]);
        self::assertSame('/backups#fetched', $res->header('Location'));
        self::assertSame(['site.tar.gz'], $this->lastCall('backup.fetched_delete')['names']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM fetched_files'));
        self::assertStringContainsString('1 fetched file was deleted', (string) $this->flash('success'));
        self::assertSame('success', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'backup.fetched_delete'"));
    }

    public function testBulkDeleteRemovesTheTickedFilesOnly(): void
    {
        $this->login('admin');
        $a = $this->record('completed', 'a.tar');
        $b = $this->record('completed', 'b.tar');
        $c = $this->record('completed', 'c.tar');
        $failed = $this->record('failed');
        $this->agent->succeed('backup.fetched_delete', ['results' => [['name' => 'a.tar', 'deleted' => true], ['name' => 'c.tar', 'deleted' => true]]]);
        $this->post('/backups/fetched/delete', ['ids' => [(string) $a, (string) $c, (string) $failed]]);
        $names = $this->lastCall('backup.fetched_delete')['names'];
        self::assertSame(['a.tar', 'c.tar'], $names, 'only files of finished fetches are sent; a failed record has no file');
        self::assertSame([$b], array_map('intval', array_column($this->db->fetchAll('SELECT id FROM fetched_files'), 'id')));
        self::assertStringContainsString('3 fetched files were deleted', (string) $this->flash('success'));
    }

    public function testAFileTheAgentCannotDeleteKeepsItsRecord(): void
    {
        $this->login('admin');
        $a = $this->record('completed', 'a.tar');
        $b = $this->record('completed', 'b.tar');
        $this->agent->succeed('backup.fetched_delete', ['results' => [['name' => 'a.tar', 'deleted' => true], ['name' => 'b.tar', 'deleted' => false, 'error' => 'permission denied']]]);
        $this->post('/backups/fetched/delete', ['ids' => [(string) $a, (string) $b]]);
        self::assertSame([$b], array_map('intval', array_column($this->db->fetchAll('SELECT id FROM fetched_files'), 'id')));
        $error = (string) $this->flash('error');
        self::assertStringContainsString('b.tar', $error);
        self::assertStringContainsString('permission denied', $error);
        self::assertStringContainsString('1 fetched file was deleted', $error);
        self::assertSame('failure', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'backup.fetched_delete' AND result = 'failure'"));
    }

    public function testAFetchStillRunningIsNotDeleted(): void
    {
        $this->login('admin');
        $running = $this->record('running');
        $this->post('/backups/fetched/delete', ['ids' => [(string) $running]]);
        self::assertSame(0, $this->calls('backup.fetched_delete'));
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM fetched_files'));
        self::assertStringContainsString('still being fetched', (string) $this->flash('error'));
    }

    public function testDeletingWithNothingTickedOrUnknownIdsChangesNothing(): void
    {
        $this->login('admin');
        $keep = $this->record('completed', 'a.tar');
        $this->post('/backups/fetched/delete', []);
        self::assertStringContainsString('Nothing was selected', (string) $this->flash('error'));
        $this->post('/backups/fetched/delete', ['ids' => ['99999', 'x', '-3']]);
        self::assertSame(0, $this->calls('backup.fetched_delete'));
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM fetched_files WHERE id = ?', [$keep]));
    }

    public function testTheTableHasCheckboxesSelectAllAndABulkButton(): void
    {
        $this->login('admin');
        $this->record('completed', 'a.tar');
        $page = $this->request('GET', '/backups')->body();
        foreach (['data-nim-select-all="ids[]"', 'name="ids[]"', 'form="fetched-bulk"', 'data-fx-bulk', 'Delete selected', 'data-nim-confirm-submit="Delete the selected files?'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        self::assertStringContainsString('id="fetched-bulk"', $page);
    }

    /** @param list<string> $a @return list<string> */
    private function sorted(array $a): array
    {
        sort($a);
        return $a;
    }
}
