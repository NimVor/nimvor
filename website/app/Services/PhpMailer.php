<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/** Plain-text mail through PHP's mail(): the web host's own mail setup does the sending. */
final class PhpMailer implements Mailer
{
    public function __construct(private readonly string $from, private readonly string $fromName)
    {
    }

    public function send(string $to, string $subject, string $body): bool
    {
        // Nothing a visitor typed may reach a header: one line, no control characters, a valid address.
        $clean = static fn (string $s): string => trim((string) preg_replace('/[\x00-\x1f\x7f]+/', ' ', $s));
        $to = $clean($to);
        if (filter_var($to, FILTER_VALIDATE_EMAIL) === false || filter_var($this->from, FILTER_VALIDATE_EMAIL) === false) {
            return false;
        }
        $headers = [
            'From: ' . $this->encodeName($clean($this->fromName)) . ' <' . $this->from . '>',
            'MIME-Version: 1.0',
            'Content-Type: text/plain; charset=UTF-8',
            'Content-Transfer-Encoding: 8bit',
            'Auto-Submitted: auto-generated',
        ];
        try {
            return @mail($to, '=?UTF-8?B?' . base64_encode($clean($subject)) . '?=', str_replace("\r\n", "\n", $body), implode("\r\n", $headers));
        } catch (\Throwable) {
            return false;
        }
    }

    private function encodeName(string $name): string
    {
        return $name === '' ? 'NimVor' : '=?UTF-8?B?' . base64_encode($name) . '?=';
    }
}
