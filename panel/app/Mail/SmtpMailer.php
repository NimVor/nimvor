<?php

declare(strict_types=1);

namespace NimVor\Mail;

use NimVor\Support\Logger;

/**
 * Minimal SMTP client (RFC 5321): implicit TLS or STARTTLS with certificate
 * verification, AUTH PLAIN/LOGIN, plain-text UTF-8 messages. Credentials
 * are never sent over an unencrypted connection except to this machine.
 */
final class SmtpMailer implements Mailer
{
    private const TIMEOUT = 15;

    /** @var resource|null */
    private $sock = null;
    private string $caps = '';

    public function __construct(
        private readonly MailConfig $config,
        private readonly Logger $log,
    ) {
    }

    public function configured(): bool
    {
        $c = $this->config->get();
        return $c['host'] !== '' && $c['from'] !== '';
    }

    public function send(array $to, string $subject, string $text): void
    {
        $c = $this->config->get();
        if (!$this->configured()) {
            throw new MailException('Outgoing email is not set up.', '', 'Enter an SMTP server under Settings → Email.');
        }
        $to = array_values(array_unique($to));
        foreach ($to as $a) {
            if (!MailConfig::validAddress($a)) {
                throw new MailException('Invalid recipient address.');
            }
        }
        if ($to === []) {
            return;
        }
        try {
            $this->connect($c);
            $this->deliver($c, $to, self::message($c['from'], $to, $subject, $text));
        } catch (MailException $e) {
            $this->log->warning('mail delivery failed', ['host' => $c['host'], 'error' => $e->getMessage(), 'reason' => $e->reason()]);
            throw $e;
        } finally {
            if (is_resource($this->sock)) {
                @fwrite($this->sock, "QUIT\r\n");
                fclose($this->sock);
            }
            $this->sock = null;
        }
    }

    /** @param array{host: string, port: int, encryption: string} $c */
    private function connect(array $c): void
    {
        $ssl = ['verify_peer' => true, 'verify_peer_name' => true, 'peer_name' => $c['host'], 'SNI_enabled' => true];
        $ctx = stream_context_create(['ssl' => $ssl]);
        $target = ($c['encryption'] === 'tls' ? 'ssl://' : 'tcp://') . (str_contains($c['host'], ':') ? '[' . $c['host'] . ']' : $c['host']) . ':' . $c['port'];
        $sock = @stream_socket_client($target, $errno, $errstr, self::TIMEOUT, STREAM_CLIENT_CONNECT, $ctx);
        if ($sock === false) {
            throw new MailException('Cannot connect to the mail server.', "{$c['host']}:{$c['port']}: {$errstr}", 'Check the host, port and encryption mode, and that outgoing connections are allowed.');
        }
        stream_set_timeout($sock, self::TIMEOUT);
        $this->sock = $sock;
        $this->expect(220);
        $ehlo = $this->ehlo();
        if ($c['encryption'] === 'starttls') {
            if (!preg_match('/^STARTTLS\b/mi', $ehlo)) {
                throw new MailException('The mail server does not offer STARTTLS.', '', 'Choose TLS (port 465), or a server that supports STARTTLS.');
            }
            $this->command('STARTTLS', 220);
            stream_context_set_option($sock, 'ssl', 'peer_name', $c['host']);
            foreach ($ssl as $k => $v) {
                stream_context_set_option($sock, 'ssl', $k, $v);
            }
            if (@stream_socket_enable_crypto($sock, true, STREAM_CRYPTO_METHOD_TLSv1_2_CLIENT | STREAM_CRYPTO_METHOD_TLSv1_3_CLIENT) !== true) {
                throw new MailException('The secure connection to the mail server failed.', 'TLS handshake or certificate verification failed.', 'Check that the host name matches the server certificate.');
            }
            $this->ehlo();
        }
    }

    private function ehlo(): string
    {
        $name = gethostname() ?: 'localhost';
        return $this->caps = $this->command('EHLO ' . (preg_match('/^[A-Za-z0-9.-]+$/', $name) ? $name : 'localhost'), 250);
    }

    /**
     * @param array{host: string, encryption: string, username: string, from: string} $c
     * @param list<string> $to
     */
    private function deliver(array $c, array $to, string $message): void
    {
        if ($c['username'] !== '') {
            $local = in_array($c['host'], ['localhost', '127.0.0.1', '::1'], true);
            if ($c['encryption'] === 'none' && !$local) {
                throw new MailException('Refusing to send the password over an unencrypted connection.');
            }
            $password = $this->config->password();
            $rejected = 'The mail server rejected the user name or password.';
            if (preg_match('/^AUTH\b(.*)$/mi', $this->caps, $m) && !preg_match('/\bPLAIN\b/i', $m[1]) && preg_match('/\bLOGIN\b/i', $m[1])) {
                $this->command('AUTH LOGIN', 334);
                $this->command(base64_encode($c['username']), 334);
                $this->command(base64_encode($password), 235, $rejected);
            } else {
                $this->command('AUTH PLAIN ' . base64_encode("\0" . $c['username'] . "\0" . $password), 235, $rejected);
            }
        }
        $this->command('MAIL FROM:<' . $c['from'] . '>', 250);
        foreach ($to as $a) {
            $this->command('RCPT TO:<' . $a . '>', [250, 251]);
        }
        $this->command('DATA', 354);
        $this->write($message . "\r\n.\r\n");
        $this->expect(250);
    }

    /** @param list<string> $to */
    public static function message(string $from, array $to, string $subject, string $text): string
    {
        $subject = trim(preg_replace('/[\r\n\t]+/', ' ', $subject) ?? '');
        $domain = substr((string) strrchr($from, '@'), 1) ?: 'localhost';
        $headers = [
            'Date: ' . date(DATE_RFC2822),
            'From: NimVor <' . $from . '>',
            'To: ' . implode(', ', $to),
            'Subject: ' . mb_encode_mimeheader($subject, 'UTF-8', 'Q', "\r\n"),
            'Message-ID: <' . bin2hex(random_bytes(12)) . '@' . $domain . '>',
            'MIME-Version: 1.0',
            'Content-Type: text/plain; charset=UTF-8',
            'Content-Transfer-Encoding: quoted-printable',
            'Auto-Submitted: auto-generated',
        ];
        $body = quoted_printable_encode(str_replace("\n", "\r\n", str_replace(["\r\n", "\r"], "\n", $text)));
        // Dot-stuffing (RFC 5321 §4.5.2).
        $body = preg_replace('/^\./m', '..', $body) ?? $body;
        return implode("\r\n", $headers) . "\r\n\r\n" . $body;
    }

    /** @param int|list<int> $ok */
    private function command(string $line, int|array $ok, string $failure = ''): string
    {
        $this->write($line . "\r\n");
        return $this->expect($ok, $failure);
    }

    private function write(string $data): void
    {
        if ($this->sock === null || @fwrite($this->sock, $data) === false) {
            throw new MailException('The connection to the mail server was lost.');
        }
    }

    /** @param int|list<int> $ok */
    private function expect(int|array $ok, string $failure = ''): string
    {
        $reply = '';
        while (($line = fgets($this->sock, 1024)) !== false) {
            $reply .= $line;
            if (strlen($line) < 4 || $line[3] === ' ') {
                break;
            }
        }
        $code = (int) substr($reply, 0, 3);
        if (!in_array($code, (array) $ok, true)) {
            $meta = stream_get_meta_data($this->sock);
            $why = $meta['timed_out'] ? 'The mail server did not answer in time.' : trim(mb_substr($reply, 0, 300));
            throw new MailException($failure !== '' ? $failure : 'The mail server refused the message.', $why);
        }
        // Drop the status codes; callers only inspect capabilities.
        return (string) preg_replace('/^\d{3}[ -]/m', '', $reply);
    }
}
