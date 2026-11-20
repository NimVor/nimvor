<?php

declare(strict_types=1);

namespace NimVor\Services;

/**
 * Checks a mail domain's DNS records (MX, SPF, DKIM, DMARC) against what
 * mail delivery needs. Pure: the records come from the panel's own DNS zone
 * (or from a live lookup) in one shape, so the same rules serve both.
 *
 * Record: ['name' => '@' | 'host' (relative to the domain), 'type' => 'TXT', 'value' => '…'].
 */
final class EmailHealth
{
    /**
     * @param array<string, mixed> $domain mail_domains row (domain, dkim_selector, dkim_txt)
     * @param list<array<string, mixed>> $records
     * @param string $mailHost the server's mail host name ('' when unknown)
     * @return list<array{code: string, level: string, title: string, detail: string, fixable: bool}>
     */
    public static function check(array $domain, array $records, string $mailHost): array
    {
        $on = array_values(array_filter($records, static fn (array $r): bool => empty($r['disabled'])));
        $by = static fn (string $name, string $type): array => array_values(array_filter($on, static fn (array $r): bool => strtolower((string) $r['name']) === strtolower($name) && strtoupper((string) $r['type']) === $type));
        $issues = [];
        $name = (string) $domain['domain'];

        // MX: where the domain's mail is delivered.
        $mx = $by('@', 'MX');
        if ($mx === []) {
            $issues[] = self::i('mx', 'bad', 'No MX record', 'Mail for ' . $name . ' cannot reach this server: there is no MX record.', true);
        } elseif ($mailHost !== '' && !self::pointsAt($mx, $mailHost)) {
            $issues[] = self::i('mx', 'warn', 'MX points elsewhere', 'The MX record does not point at this server (' . $mailHost . '); mail is delivered somewhere else.', false);
        }

        // SPF: which servers may send for the domain.
        $spf = array_values(array_filter($by('@', 'TXT'), static fn (array $r): bool => str_starts_with(strtolower(trim((string) $r['value'], "\" ")), 'v=spf1')));
        if ($spf === []) {
            $issues[] = self::i('spf', 'warn', 'No SPF record', 'Without SPF, receivers are more likely to mark mail from ' . $name . ' as spam.', true);
        } elseif (count($spf) > 1) {
            $issues[] = self::i('spf', 'bad', 'Several SPF records', 'There are ' . count($spf) . ' SPF records; receivers treat that as an error. Merge them into one.', false);
        } elseif (preg_match('/\+all\b/i', (string) $spf[0]['value']) === 1) {
            $issues[] = self::i('spf', 'bad', 'SPF allows everyone', 'The SPF record ends in +all, which lets anyone send mail as this domain.', false);
        }

        // DKIM: the public key matching what this server signs with.
        $selector = (string) ($domain['dkim_selector'] ?? '');
        if ($selector !== '') {
            $dkim = $by($selector . '._domainkey', 'TXT');
            if ($dkim === []) {
                $issues[] = self::i('dkim', 'warn', 'No DKIM record', 'The DKIM key (' . $selector . '._domainkey) is not published, so mail from ' . $name . ' is not signed in the eyes of receivers.', true);
            } else {
                $want = self::dkimKey((string) ($domain['dkim_txt'] ?? ''));
                $have = self::dkimKey((string) $dkim[0]['value']);
                if ($want !== '' && $have !== '' && $want !== $have) {
                    $issues[] = self::i('dkim', 'bad', 'DKIM key is out of date', 'The published DKIM key is not the one this server signs with; signatures fail. Publish the key shown on the Email page.', false);
                }
            }
        }

        // DMARC: what receivers do with mail that fails SPF and DKIM.
        $dmarc = array_values(array_filter($by('_dmarc', 'TXT'), static fn (array $r): bool => str_starts_with(strtolower(trim((string) $r['value'], "\" ")), 'v=dmarc1')));
        if ($dmarc === []) {
            $issues[] = self::i('dmarc', 'warn', 'No DMARC record', 'Add a DMARC record so receivers know how to treat forged mail from ' . $name . '.', true);
        } elseif (preg_match('/\bp\s*=\s*none\b/i', (string) $dmarc[0]['value']) === 1) {
            $issues[] = self::i('dmarc', 'info', 'DMARC only monitors', 'The policy is p=none: nothing is rejected. Tighten it to quarantine once your mail passes.', false);
        }
        return $issues;
    }

    /** @param list<array<string, mixed>> $mx */
    private static function pointsAt(array $mx, string $mailHost): bool
    {
        foreach ($mx as $r) {
            $t = strtolower(rtrim((string) $r['value'], '.'));
            if ($t === strtolower($mailHost) || $t === 'mail' || $t === '@') {
                return true;
            }
        }
        return false;
    }

    /** The public key (p=…) of a DKIM record, without quotes and spaces. */
    private static function dkimKey(string $txt): string
    {
        return preg_match('/p=([A-Za-z0-9+\/=\s"]+)/', $txt, $m) === 1 ? preg_replace('/[\s"]+/', '', $m[1]) ?? '' : '';
    }

    /** @return array{code: string, level: string, title: string, detail: string, fixable: bool} */
    private static function i(string $code, string $level, string $title, string $detail, bool $fixable): array
    {
        return ['code' => $code, 'level' => $level, 'title' => $title, 'detail' => $detail, 'fixable' => $fixable];
    }
}
