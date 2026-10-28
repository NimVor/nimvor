<?php

declare(strict_types=1);

namespace NimVor\Services;

/**
 * Per-website security and health checks for the customer portal. Each check
 * is a plain result the view renders with a status, an explanation and how to
 * fix it (and whether the customer can fix it themselves or should ask their
 * provider). Everything here reads the stored state or the panel database;
 * the one agent-backed lookup (the certificate) already fails safe.
 */
final class SiteHealthService
{
    public function __construct(
        private readonly SslService $ssl,
        private readonly AvService $av,
        private readonly SiteOverview $overview,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return list<array{level: string, title: string, detail: string, fix: string, where: string}>
     */
    public function security(array $site): array
    {
        $checks = [];
        $https = !empty($site['https']);
        $force = !empty($site['force_https']);
        if (!$https) {
            $checks[] = $this->c('bad', 'HTTPS is off', 'Visitors reach your site over an unencrypted connection.', 'Issue a free certificate on the SSL / HTTPS page, then turn on Force HTTPS.', 'you');
        } elseif (!$force) {
            $checks[] = $this->c('warn', 'Visitors can still use plain http://', 'HTTPS works, but requests are not redirected to it.', 'Turn on Force HTTPS on the SSL / HTTPS page.', 'you');
        } else {
            $checks[] = $this->c('ok', 'HTTPS is on and enforced', 'All traffic is encrypted and redirected to HTTPS.', '', 'you');
        }

        if ($https) {
            $cert = $this->ssl->certificate($site);
            $days = $cert === null ? null : (int) ($cert['days_remaining'] ?? $cert['days'] ?? 0);
            if ($days !== null && $days <= 15) {
                $checks[] = $this->c($days <= 3 ? 'bad' : 'warn', 'Your certificate expires soon', 'It is valid for ' . max(0, $days) . ' more day(s).', 'Let\'s Encrypt certificates renew automatically; if you uploaded your own, upload a fresh one on the SSL / HTTPS page.', 'you');
            } elseif ($days !== null) {
                $checks[] = $this->c('ok', 'Certificate is valid', 'Valid for ' . $days . ' more day(s).', '', 'you');
            }
        }

        $scan = $this->av->history((int) $site['id'], 1)[0] ?? null;
        if ($scan !== null && (int) ($scan['infected'] ?? 0) > 0) {
            $checks[] = $this->c('bad', 'Malware was found in your files', (int) $scan['infected'] . ' infected file(s) in the last scan.', 'Review and remove them on the Malware scan page, then rescan.', 'you');
        } elseif ($scan === null) {
            $checks[] = $this->c('warn', 'No malware scan has run yet', 'Your files have not been scanned for malware.', 'Run a scan on the Malware scan page.', 'you');
        } else {
            $checks[] = $this->c('ok', 'No malware found', 'The last scan found nothing.', '', 'you');
        }

        if (($site['kind'] ?? '') === 'php' && (string) ($site['php_version'] ?? '') !== '' && version_compare((string) $site['php_version'], '8.1', '<')) {
            $checks[] = $this->c('warn', 'PHP ' . $site['php_version'] . ' no longer gets security updates', 'Old PHP versions stop receiving fixes.', 'Choose PHP 8.1 or newer under PHP & settings (check your apps support it first).', 'you');
        }

        return $checks;
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $user
     * @return list<array{level: string, title: string, detail: string, fix: string, where: string}>
     */
    public function health(array $site, array $user): array
    {
        $checks = [];
        if (empty($site['enabled'])) {
            $checks[] = $this->c('bad', 'Your website is turned off', 'It is not being served to visitors.', 'Ask your hosting provider to re-enable it.', 'admin');
        } else {
            $checks[] = $this->c('ok', 'Your website is active', 'It is enabled and served.', '', 'you');
        }

        $summary = $this->overview->summary($site, $user);
        $last = $summary['last_backup'] ?? null;
        if ($last === null) {
            $checks[] = $this->c('warn', 'No successful backup yet', 'You have no backup to restore from if something goes wrong.', 'Create a backup on the Backups page.', 'you');
        } else {
            $checks[] = $this->c('ok', 'You have a recent backup', 'Last backup: ' . (string) ($last['created_at'] ?? ''), '', 'you');
        }

        return $checks;
    }

    /**
     * @return array{level: string, title: string, detail: string, fix: string, where: string}
     */
    private function c(string $level, string $title, string $detail, string $fix, string $where): array
    {
        return ['level' => $level, 'title' => $title, 'detail' => $detail, 'fix' => $fix, 'where' => $where];
    }
}
