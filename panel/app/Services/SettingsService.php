<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Repositories\SettingRepository;
use WhaCP\Support\ValidationException;

/**
 * Typed, validated panel settings stored in SQLite.
 */
final class SettingsService
{
    /** @var array<string, mixed>|null */
    private ?array $cache = null;

    public function __construct(
        private readonly SettingRepository $repository,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * Setting definitions: type, default, constraints and labels.
     *
     * @return array<string, array{type: string, default: mixed, label: string, help: string, min?: int, max?: int, max_length?: int}>
     */
    public static function definitions(): array
    {
        return [
            'panel_name' => [
                'category' => 'general', 'type' => 'string', 'default' => 'WhaCP', 'max_length' => 60,
                'label' => 'Panel name', 'help' => 'Shown in the header and browser title.',
            ],
            'panel_url' => [
                'category' => 'general', 'type' => 'url', 'default' => '', 'max_length' => 200,
                'label' => 'Public panel URL', 'help' => 'e.g. https://panel.example.com. Used for Git webhook URLs, password reset links and Google Drive; when empty the address you are browsing is used.',
            ],
            'session_idle_minutes' => [
                'category' => 'security', 'type' => 'int', 'default' => 30, 'min' => 5, 'max' => 1440,
                'label' => 'Session idle timeout (minutes)', 'help' => 'Users are signed out after this period of inactivity.',
            ],
            'discovery_cache_seconds' => [
                'category' => 'general', 'type' => 'int', 'default' => 300, 'min' => 0, 'max' => 86400,
                'label' => 'Discovery cache (seconds)', 'help' => 'How long a discovery snapshot is reused before the agent is asked again. 0 always asks the agent.',
            ],
            'dashboard_refresh_seconds' => [
                'category' => 'general', 'type' => 'int', 'default' => 10, 'min' => 3, 'max' => 300,
                'label' => 'Dashboard refresh interval (seconds)', 'help' => 'How often the dashboard polls live metrics.',
            ],
            'alert_disk_percent' => [
                'category' => 'monitoring', 'type' => 'int', 'default' => 90, 'min' => 50, 'max' => 99,
                'label' => 'Disk usage alert (%)', 'help' => 'Alert when any disk is fuller than this.',
            ],
            'alert_memory_percent' => [
                'category' => 'monitoring', 'type' => 'int', 'default' => 95, 'min' => 50, 'max' => 100,
                'label' => 'Memory usage alert (%)', 'help' => 'Alert when memory use stays above this for two samples in a row.',
            ],
            'alert_load_per_core' => [
                'category' => 'monitoring', 'type' => 'int', 'default' => 2, 'min' => 1, 'max' => 20,
                'label' => 'Load alert (per CPU core)', 'help' => 'Alert when the 1-minute load exceeds this many times the number of cores.',
            ],
            'alert_webhook_url' => [
                'category' => 'monitoring', 'type' => 'url', 'default' => '', 'max_length' => 500,
                'label' => 'Alert webhook URL', 'help' => 'Optional https:// URL that receives alerts as JSON ({"text": …}); works with Slack, Mattermost, Discord (/slack) and ntfy.',
            ],
            'dns_nameservers' => [
                'category' => 'dns', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 300,
                'label' => 'DNS name servers', 'help' => 'Host names of the name servers for hosted zones, separated by commas (e.g. ns1.example.com, ns2.example.com). New zones get NS records for them.',
            ],
            'dns_hostmaster' => [
                'category' => 'dns', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 200,
                'label' => 'DNS hostmaster email', 'help' => 'Contact address in the SOA record of new zones. Empty uses hostmaster@<zone>.',
            ],
            'dns_default_ipv4' => [
                'category' => 'dns', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 15,
                'label' => 'Default IPv4 for new zones', 'help' => 'The public address new zones point @ and www to. Empty leaves the A records out.',
            ],
            'auto_ssl' => [
                'category' => 'ssl', 'type' => 'bool', 'default' => true,
                'label' => 'Automatic SSL for new websites', 'help' => 'Issue a Let\'s Encrypt certificate for a new website once its domain points to this server (its name servers are this panel\'s, or its address is this server\'s). Checked every few minutes for up to 3 days.',
            ],
            'auto_ssl_replace' => [
                'category' => 'ssl', 'type' => 'bool', 'default' => true,
                'label' => 'Replace expired or invalid certificates', 'help' => 'Every few hours, a website whose certificate has expired, expires within 7 days without being renewed, does not cover its domain or is self-signed gets a new Let\'s Encrypt certificate once its domain points to this server. Every attempt is logged on SSL/TLS → AutoSSL.',
            ],
            'av_scan_days' => [
                'category' => 'security', 'type' => 'int', 'default' => 0, 'min' => 0, 'max' => 30,
                'label' => 'Scheduled malware scans (every N days)', 'help' => 'Scan every website with ClamAV every this many days, as the website\'s account, at night. 0 turns scheduled scans off.',
            ],
            'av_quarantine' => [
                'category' => 'security', 'type' => 'bool', 'default' => false,
                'label' => 'Quarantine infected files', 'help' => 'Move files a malware scan finds into the website\'s .whacp-quarantine folder (unreadable) instead of only reporting them.',
            ],
            'terminal_idle_minutes' => [
                'category' => 'security', 'type' => 'int', 'default' => 15, 'min' => 1, 'max' => 240,
                'label' => 'Web terminal idle timeout (minutes)', 'help' => 'A web terminal closes after this many minutes without input.',
            ],
            'terminal_max_minutes' => [
                'category' => 'security', 'type' => 'int', 'default' => 480, 'min' => 15, 'max' => 1440,
                'label' => 'Web terminal maximum session (minutes)', 'help' => 'A web terminal closes after this long, active or not.',
            ],
            'modsec_backup_retention_days' => [
                'category' => 'security', 'type' => 'int', 'default' => 30, 'min' => 1, 'max' => 3650,
                'label' => 'ModSecurity installation backups: keep (days)', 'help' => 'Backups and records of ModSecurity connector and rule installations are kept this long. The one in effect (the last known-good) is always kept.',
            ],
            'db_customer_remote_hosts' => [
                'category' => 'security', 'type' => 'bool', 'default' => true,
                'label' => 'Hosting customers may allow remote database connections', 'help' => 'Customers and resellers may let their database users connect from other hosts. Off: from this server only (administrators still can). Existing remote logins are kept.',
            ],
            'php_harden_new_sites' => [
                'category' => 'security', 'type' => 'bool', 'default' => false,
                'label' => 'Disable shell functions for new websites', 'help' => 'New websites start with exec, system, shell_exec, passthru, proc_open, popen and pcntl_exec disabled. Some applications (for example Composer or queue workers run from PHP) need them; an administrator can turn this off per website.',
            ],
            'wordpress_auto_harden' => [
                'category' => 'security', 'type' => 'bool', 'default' => true,
                'label' => 'Harden new WordPress installations automatically', 'help' => 'WordPress installed with the app installer gets the recommended security measures (web server rules and WordPress settings) as soon as it is installed. Each measure can be rolled back in the WordPress toolkit.',
            ],
            // Security policies (Security → Configure Security Policies and
            // Password Strength Configuration; not on the Settings page).
            'policy_password_strength' => [
                'category' => 'policy', 'type' => 'bool', 'default' => true,
                'label' => 'Password Strength', 'help' => 'New passwords must meet the Password Strength Configuration.',
            ],
            'policy_password_age_days' => [
                'category' => 'policy', 'type' => 'int', 'default' => 0, 'min' => 0, 'max' => 3650,
                'label' => 'Password Age (days)', 'help' => 'Users must choose a new password after this many days. 0 turns it off.',
            ],
            'policy_verified_ips' => [
                'category' => 'policy', 'type' => 'bool', 'default' => false,
                'label' => 'Limit logins to verified IP addresses', 'help' => 'A sign-in from an IP address the user has not used before needs a code sent to the user\'s email address.',
            ],
            'password_min_strength' => [
                'category' => 'policy', 'type' => 'int', 'default' => 65, 'min' => 0, 'max' => 100,
                'label' => 'Minimum password strength', 'help' => '0–100, from the length and the kinds of characters used.',
            ],
            'password_min_length' => [
                'category' => 'policy', 'type' => 'int', 'default' => 12, 'min' => 12, 'max' => 128,
                'label' => 'Total characters', 'help' => 'At least 12.',
            ],
            'password_min_special' => [
                'category' => 'policy', 'type' => 'int', 'default' => 0, 'min' => 0, 'max' => 32,
                'label' => 'Special characters', 'help' => 'Characters other than letters and digits.',
            ],
            'password_min_numeric' => [
                'category' => 'policy', 'type' => 'int', 'default' => 0, 'min' => 0, 'max' => 32,
                'label' => 'Numeric characters', 'help' => '0–9.',
            ],
            'password_min_alpha' => [
                'category' => 'policy', 'type' => 'int', 'default' => 0, 'min' => 0, 'max' => 32,
                'label' => 'Alphabet characters', 'help' => 'Letters, upper or lower case.',
            ],
            'password_min_upper' => [
                'category' => 'policy', 'type' => 'int', 'default' => 0, 'min' => 0, 'max' => 32,
                'label' => 'Uppercase characters', 'help' => 'A–Z.',
            ],
            'password_min_lower' => [
                'category' => 'policy', 'type' => 'int', 'default' => 0, 'min' => 0, 'max' => 32,
                'label' => 'Lowercase characters', 'help' => 'a–z.',
            ],
            // Brute force protection (Security → cPHulk Brute Force Protection; not on the Settings page).
            'hulk_enabled' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => false,
                'label' => 'cPHulk Brute Force Protection', 'help' => '',
            ],
            'hulk_user_enabled' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => true,
                'label' => 'Username-based Protection', 'help' => '',
            ],
            'hulk_user_period' => [
                'category' => 'hulk', 'type' => 'int', 'default' => 5, 'min' => 1, 'max' => 1440,
                'label' => 'Brute Force Protection Period (in minutes)', 'help' => '',
            ],
            'hulk_user_max' => [
                'category' => 'hulk', 'type' => 'int', 'default' => 5, 'min' => 1, 'max' => 1000,
                'label' => 'Maximum Failures by Account', 'help' => '',
            ],
            'hulk_user_scope' => [
                'category' => 'hulk', 'type' => 'string', 'default' => 'all', 'max_length' => 5,
                'label' => 'Protection type', 'help' => 'local or all',
            ],
            'hulk_user_lock_root' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => false,
                'label' => 'Allow username protection to lock the “root” user', 'help' => '',
            ],
            'hulk_ip_enabled' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => true,
                'label' => 'IP Address-based Protection', 'help' => '',
            ],
            'hulk_ip_period' => [
                'category' => 'hulk', 'type' => 'int', 'default' => 15, 'min' => 1, 'max' => 1440,
                'label' => 'IP Address-based Brute Force Protection Period (in minutes)', 'help' => '',
            ],
            'hulk_ip_max' => [
                'category' => 'hulk', 'type' => 'int', 'default' => 15, 'min' => 1, 'max' => 1000,
                'label' => 'Maximum Failures per IP Address', 'help' => '',
            ],
            'hulk_ip_command' => [
                'category' => 'hulk', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 500,
                'label' => 'Command to Run When an IP Address Triggers Brute Force Protection', 'help' => '',
            ],
            'hulk_ip_firewall' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => false,
                'label' => 'Block IP addresses at the firewall level if they trigger brute force protection', 'help' => '',
            ],
            'hulk_oneday_max' => [
                'category' => 'hulk', 'type' => 'int', 'default' => 30, 'min' => 1, 'max' => 10000,
                'label' => 'Maximum Failures per IP Address before the IP Address is Blocked for One Day', 'help' => '',
            ],
            'hulk_oneday_command' => [
                'category' => 'hulk', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 500,
                'label' => 'Command to Run When an IP Address Triggers a One-Day Block', 'help' => '',
            ],
            'hulk_oneday_firewall' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => false,
                'label' => 'Block IP addresses at the firewall level if they trigger a one-day block', 'help' => '',
            ],
            'hulk_retention' => [
                'category' => 'hulk', 'type' => 'int', 'default' => 360, 'min' => 5, 'max' => 10080,
                'label' => 'Duration for Retaining Failed Logins (in minutes)', 'help' => '',
            ],
            'hulk_notify_root' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => true,
                'label' => 'Send a notification upon successful root login when the IP address is not on the whitelist', 'help' => '',
            ],
            'hulk_notify_root_netblock' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => false,
                'label' => 'Send a notification upon successful root login when the IP address is not on the whitelist, but from a known netblock', 'help' => '',
            ],
            'hulk_notify_brute' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => true,
                'label' => 'Send a notification when the system detects a brute force user', 'help' => '',
            ],
            'php_profile' => [
                'category' => 'server', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 2000,
                'label' => 'PHP profile', 'help' => 'PHP Manager: the PHP versions and extensions last applied (JSON).',
            ],
            'setup_wizard_seen' => [
                'category' => 'server', 'type' => 'bool', 'default' => false,
                'label' => 'Setup wizard opened', 'help' => 'The dashboard opens the setup wizard once, for the first administrator visit.',
            ],
            'setup_wizard_done' => [
                'category' => 'server', 'type' => 'bool', 'default' => false,
                'label' => 'Setup wizard closed', 'help' => 'The dashboard no longer offers the setup wizard.',
            ],
            'server_setup' => [
                'category' => 'server', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 12000,
                'label' => 'Server setup', 'help' => 'The last run of the server setup (profile, options, each step\'s result; JSON).',
            ],
            'components_expected' => [
                'category' => 'server', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 300,
                'label' => 'Components this server should have', 'help' => 'Server → Components. Empty: the installed ones.',
            ],
            'hulk_countries_all' => [
                'category' => 'hulk', 'type' => 'bool', 'default' => false,
                'label' => 'Block blacklisted countries on every port', 'help' => 'Like CSF\'s CC_DENY: blacklisted countries cannot reach any service, websites included. Otherwise only WhaCP and the login services.',
            ],
            'hulk_whitelist' => [
                'category' => 'hulk', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 4000,
                'label' => 'Whitelisted IP addresses', 'help' => '',
            ],
            'hulk_synced' => [
                'category' => 'hulk', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 5,
                'label' => 'Brute force protection rules applied', 'help' => '',
            ],
            'hulk_geo_updated' => [
                'category' => 'hulk', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 20,
                'label' => 'Country database date', 'help' => '',
            ],
            'hulk_geo_tried' => [
                'category' => 'hulk', 'type' => 'string', 'optional' => true, 'default' => '', 'max_length' => 20,
                'label' => 'Country database download attempt', 'help' => '',
            ],
            'trash_retention_days' => [
                'category' => 'general', 'type' => 'int', 'default' => 30, 'min' => 0, 'max' => 365,
                'label' => 'File Manager trash: keep deleted files (days)', 'help' => 'Files deleted in the File Manager can be restored from its Trash for this many days, then they are deleted for good. 0 keeps them until the trash is emptied.',
            ],
            'ssl_warning_days' => [
                'category' => 'ssl', 'type' => 'int', 'default' => 14, 'min' => 1, 'max' => 90,
                'label' => 'Certificate warning threshold (days)', 'help' => 'Certificates expiring within this many days are highlighted.',
            ],
        ];
    }

    public function get(string $key): mixed
    {
        $defs = self::definitions();
        if (!isset($defs[$key])) {
            throw new \InvalidArgumentException("Unknown setting {$key}");
        }
        $this->cache ??= $this->repository->all();
        return $this->cache[$key] ?? $defs[$key]['default'];
    }

    public function bool(string $key): bool
    {
        return filter_var($this->get($key), FILTER_VALIDATE_BOOL);
    }

    public function int(string $key): int
    {
        return (int) $this->get($key);
    }

    public function string(string $key): string
    {
        return (string) $this->get($key);
    }

    /** @return array<string, mixed> */
    public function all(): array
    {
        $out = [];
        foreach (array_keys(self::definitions()) as $key) {
            $out[$key] = $this->get($key);
        }
        return $out;
    }

    /**
     * Validate and persist settings. Unknown keys are ignored.
     *
     * @param array<string, mixed> $input
     */
    public function update(array $input, ?int $userId): void
    {
        $errors = [];
        $clean = [];
        foreach (self::definitions() as $key => $def) {
            if (!array_key_exists($key, $input)) {
                continue;
            }
            $raw = $input[$key];
            if ($def['type'] === 'bool') {
                $clean[$key] = filter_var($raw, FILTER_VALIDATE_BOOL);
            } elseif ($def['type'] === 'int') {
                $v = filter_var($raw, FILTER_VALIDATE_INT);
                if ($v === false || $v < ($def['min'] ?? PHP_INT_MIN) || $v > ($def['max'] ?? PHP_INT_MAX)) {
                    $errors[$key] = sprintf('Enter a whole number between %d and %d.', $def['min'] ?? 0, $def['max'] ?? 0);
                    continue;
                }
                $clean[$key] = $v;
            } elseif ($def['type'] === 'url') {
                // Optional; https only (plain http only for this machine).
                $v = is_scalar($raw) ? rtrim(trim((string) $raw), '/') : '';
                $ok = $v === '' || (mb_strlen($v) <= ($def['max_length'] ?? 255) && filter_var($v, FILTER_VALIDATE_URL) !== false
                    && preg_match('#^(https://|http://(localhost|127\.0\.0\.1)(:\d+)?(/|$))#i', $v) && !preg_match('/[\s"\'<>]/', $v));
                if (!$ok) {
                    $errors[$key] = 'Enter an https:// address, or leave it empty.';
                    continue;
                }
                $clean[$key] = $v;
            } else {
                $v = is_scalar($raw) ? trim((string) $raw) : '';
                if (($v === '' && empty($def['optional'])) || mb_strlen($v) > ($def['max_length'] ?? 255) || preg_match('/[\x00-\x1F\x7F]/u', $v)) {
                    $errors[$key] = sprintf('Enter between 1 and %d printable characters.', $def['max_length'] ?? 255);
                    continue;
                }
                $clean[$key] = $v;
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $before = $this->all();
        $changed = [];
        foreach ($clean as $key => $value) {
            if ($before[$key] !== $value) {
                $this->repository->set($key, $value, $userId);
                $changed[$key] = ['from' => $before[$key], 'to' => $value];
            }
        }
        $this->cache = null;
        if ($changed !== []) {
            $this->audit->record('settings.update', 'settings', 'panel', AuditLogger::SUCCESS, ['changed' => $changed]);
        }
    }
}
