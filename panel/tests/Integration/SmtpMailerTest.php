<?php

declare(strict_types=1);

namespace NimVor\Tests\Integration;

use NimVor\Mail\MailConfig;
use NimVor\Mail\MailException;
use NimVor\Mail\SmtpMailer;
use NimVor\Security\Crypto;
use NimVor\Support\Logger;
use NimVor\Support\ValidationException;
use NimVor\Tests\Support\DatabaseTestCase;

final class SmtpMailerTest extends DatabaseTestCase
{
    private string $dir;
    /** @var resource|null */
    private $proc = null;

    protected function setUp(): void
    {
        parent::setUp();
        $this->dir = sys_get_temp_dir() . '/nimvor-smtp-' . bin2hex(random_bytes(4));
        mkdir($this->dir);
    }

    protected function tearDown(): void
    {
        if (is_resource($this->proc)) {
            proc_terminate($this->proc);
        }
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    private function config(): MailConfig
    {
        return new MailConfig(new \NimVor\Repositories\SettingRepository($this->db), new Crypto($this->dir . '/key'), $this->audit);
    }

    /** Start the fake server; returns its port. */
    private function server(string $mode): int
    {
        $this->proc = proc_open([PHP_BINARY, dirname(__DIR__) . '/Support/fake_smtp.php', $this->dir . '/transcript', $mode], [1 => ['pipe', 'w'], 2 => ['file', '/dev/null', 'a']], $pipes);
        return (int) fgets($pipes[1]);
    }

    private function transcript(): string
    {
        for ($i = 0; $i < 50 && !is_file($this->dir . '/transcript'); $i++) {
            usleep(100_000);
        }
        return (string) file_get_contents($this->dir . '/transcript');
    }

    public function testDeliversWithAuthAndDotStuffing(): void
    {
        $port = $this->server('plain');
        $cfg = $this->config();
        $cfg->save(['host' => '127.0.0.1', 'port' => (string) $port, 'encryption' => 'none', 'username' => 'mailer', 'password' => 's3cret', 'from' => 'panel@example.com', 'alert_to' => 'ops@example.com, oncall@example.com'], null);
        self::assertSame(['ops@example.com', 'oncall@example.com'], $cfg->alertRecipients());
        self::assertStringNotContainsString('s3cret', (string) json_encode($this->db->fetchAll('SELECT * FROM settings')), 'password stored encrypted');

        $mailer = new SmtpMailer($cfg, new Logger($this->dir . '/log', 'debug'));
        self::assertTrue($mailer->configured());
        $mailer->send(['ops@example.com'], "Disk full\r\nBcc: evil@example.com", "Line one\n.hidden dot line\nÜnïcode");
        $t = $this->transcript();
        self::assertStringContainsString('C: AUTH PLAIN ' . base64_encode("\0mailer\0s3cret"), $t);
        self::assertStringContainsString('C: MAIL FROM:<panel@example.com>', $t);
        self::assertStringContainsString('C: RCPT TO:<ops@example.com>', $t);
        self::assertStringContainsString("C: ..hidden dot line", $t, 'dot-stuffed');
        self::assertStringNotContainsString("\nC: Bcc:", $t, 'no header injection through the subject');
        self::assertStringContainsString('C: Content-Type: text/plain; charset=UTF-8', $t);
    }

    public function testRejectedCredentialsAreReported(): void
    {
        $port = $this->server('plain');
        $cfg = $this->config();
        $cfg->save(['host' => 'localhost', 'port' => (string) $port, 'encryption' => 'none', 'username' => 'mailer', 'password' => 'wrong', 'from' => 'panel@example.com'], null);
        try {
            (new SmtpMailer($cfg, new Logger($this->dir . '/log', 'debug')))->send(['ops@example.com'], 's', 't');
            self::fail('expected a failure');
        } catch (MailException $e) {
            self::assertStringContainsString('user name or password', $e->getMessage());
        }
    }

    public function testStartTlsVerifiesTheCertificate(): void
    {
        $port = $this->server('starttls');
        $cfg = $this->config();
        $cfg->save(['host' => '127.0.0.1', 'port' => (string) $port, 'encryption' => 'starttls', 'username' => 'mailer', 'password' => 's3cret', 'from' => 'panel@example.com'], null);
        try {
            (new SmtpMailer($cfg, new Logger($this->dir . '/log', 'debug')))->send(['ops@example.com'], 's', 't');
            self::fail('a self-signed certificate must be refused');
        } catch (MailException $e) {
            self::assertStringContainsString('secure connection', $e->getMessage());
        }
        self::assertStringNotContainsString('AUTH', $this->transcript(), 'credentials never sent');
    }

    public function testValidation(): void
    {
        $cfg = $this->config();
        try {
            $cfg->save(['host' => 'smtp.example.com', 'port' => '25', 'encryption' => 'none', 'from' => 'panel@example.com'], null);
            self::fail('plain SMTP to a remote host must be refused');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('encryption', $e->errors());
        }
        try {
            $cfg->save(['host' => 'smtp.example.com', 'port' => '587', 'encryption' => 'starttls', 'from' => "a@b.c\r\nBcc: x@y.z", 'alert_to' => 'nope'], null);
            self::fail('bad addresses must be refused');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('from', $e->errors());
            self::assertArrayHasKey('alert_to', $e->errors());
        }
    }
}
