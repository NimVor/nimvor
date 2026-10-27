<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Support\UserFacingException;

/**
 * Firewall, SSH and fail2ban. Firewall and SSH changes are applied by the
 * agent under a guard and reverted automatically unless confirmed within
 * two minutes, so a change that cuts off access undoes itself.
 */
final class SecurityService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ?HulkService $hulk = null,
    ) {
    }

    /** @return array<string, mixed>|null */
    public function pending(): ?array
    {
        try {
            $p = ((array) $this->agent->call('security.pending'))['pending'] ?? null;
        } catch (AgentException) {
            return null;
        }
        return is_array($p) ? $p : null;
    }

    /** @param array<string, mixed> $res */
    private function recordGuarded(string $action, array $res, array $details): string
    {
        $p = (array) ($res['pending'] ?? []);
        $this->audit->record($action, 'server', 'security', AuditLogger::SUCCESS, $details + ['pending' => $p['id'] ?? null]);
        return empty($res['changed']) ? 'Nothing to change.' : 'Applied. Check that you can still connect, then confirm within 2 minutes — otherwise the change is undone automatically.';
    }

    /** @param array<string, mixed> $input */
    public function addRule(array $input): string
    {
        $rule = [
            'action' => (string) ($input['action'] ?? 'allow'),
            'port' => trim((string) ($input['port'] ?? '')),
            'proto' => (string) ($input['proto'] ?? ''),
            'from' => trim((string) ($input['from'] ?? '')),
            'comment' => trim((string) ($input['comment'] ?? '')),
        ];
        return $this->recordGuarded('firewall.rule_add', (array) $this->agent->call('firewall.rule_add', $rule), $rule);
    }

    public function deleteRule(string $spec): string
    {
        return $this->recordGuarded('firewall.rule_delete', (array) $this->agent->call('firewall.rule_delete', ['spec' => $spec]), ['rule' => $spec]);
    }

    /**
     * Turn the firewall on or off. Turning it on first opens SSH and the
     * panel (always), the listening services' ports ($services) and every
     * port for the administrator's address ($myIp, also whitelisted in
     * brute force protection), all in the same guarded change.
     */
    public function setFirewall(bool $enabled, bool $services = true, string $myIp = ''): string
    {
        if (!$enabled) {
            return $this->recordGuarded('firewall.disable', (array) $this->agent->call('firewall.set_enabled', ['enabled' => false]), []);
        }
        $ip = self::publicAddress($myIp) ? $myIp : '';
        $params = ['enabled' => true, 'allow_services' => $services, 'allow_from' => $ip];
        $msg = $this->recordGuarded('firewall.enable', (array) $this->agent->call('firewall.set_enabled', $params, 90), $params);
        if ($ip !== '' && $this->hulk !== null) {
            $this->hulk->addToWhitelist($ip);
        }
        return $msg;
    }

    /**
     * Firewall protections (CSF-style): the form's values, checked and sent
     * to the agent as one guarded change. $myIp, when given, is exempted.
     *
     * @param array<string, mixed> $in
     */
    public function setProtection(array $in, string $myIp = ''): string
    {
        $num = static function (mixed $v, string $what, int $max): int {
            $v = trim((string) $v);
            if ($v === '') {
                return 0;
            }
            if (!preg_match('/^\d{1,7}$/', $v) || (int) $v > $max) {
                throw new UserFacingException(sprintf('%s: a number from 0 to %d.', $what, $max));
            }
            return (int) $v;
        };
        $p = [
            'enabled' => ($in['enabled'] ?? '') === '1',
            'invalid' => ($in['invalid'] ?? '') === '1',
            'syn_rate' => $num($in['syn_rate'] ?? '', 'SYN flood', 100000),
            'icmp_rate' => $num($in['icmp_rate'] ?? '', 'Ping', 10000),
            'conn_limits' => self::portLimits((string) ($in['conn_limits'] ?? ''), false, 'Connection limits'),
            'port_flood' => self::portLimits((string) ($in['port_flood'] ?? ''), true, 'Port flood'),
            'exempt' => array_values(array_filter(preg_split('/[\s,]+/', (string) ($in['exempt'] ?? '')) ?: [], static fn (string $e): bool => $e !== '')),
        ];
        foreach ($p['exempt'] as $e) {
            if (!HulkService::validEntry($e)) {
                throw new UserFacingException(sprintf('Exempt: "%s" is not an IP address or network.', mb_substr($e, 0, 60)));
            }
        }
        if (self::publicAddress($myIp) && !in_array($myIp, $p['exempt'], true)) {
            $p['exempt'][] = $myIp;
        }
        return $this->recordGuarded('firewall.protection', (array) $this->agent->call('firewall.protection_set', $p, 90), $p);
    }

    /**
     * "22:10, 80:150" (connection limits) or "tcp/80:300, udp/53:600" (new
     * connections per minute).
     *
     * @return list<array{port: int, proto: string, max: int}>
     */
    public static function portLimits(string $text, bool $withProto, string $what): array
    {
        $out = [];
        foreach (preg_split('/[\s,;]+/', trim($text)) ?: [] as $item) {
            if ($item === '') {
                continue;
            }
            $re = $withProto ? '/^(?:(tcp|udp)\/)?(\d{1,5}):(\d{1,7})$/i' : '/^()(\d{1,5}):(\d{1,6})$/';
            if (!preg_match($re, $item, $m) || (int) $m[2] < 1 || (int) $m[2] > 65535 || (int) $m[3] < 1) {
                throw new UserFacingException(sprintf('%s: "%s" is not %s.', $what, mb_substr($item, 0, 40), $withProto ? 'like tcp/80:300' : 'like 22:10'));
            }
            $out[] = ['port' => (int) $m[2], 'proto' => strtolower($m[1] !== '' ? $m[1] : 'tcp'), 'max' => (int) $m[3]];
        }
        if (count($out) > 50) {
            throw new UserFacingException($what . ': at most 50 ports.');
        }
        return $out;
    }

    /** An address worth allowing (not loopback, private tunnels are fine). */
    public static function publicAddress(string $ip): bool
    {
        return filter_var($ip, FILTER_VALIDATE_IP) !== false && !str_starts_with($ip, '127.') && $ip !== '::1';
    }

    /** @param array<string, mixed> $input */
    public function configureSsh(array $input): string
    {
        $s = [
            'port' => (int) ($input['port'] ?? 22),
            'permit_root_login' => (string) ($input['permit_root_login'] ?? 'prohibit-password'),
            'password_authentication' => filter_var($input['password_authentication'] ?? false, FILTER_VALIDATE_BOOL),
            'max_auth_tries' => (int) ($input['max_auth_tries'] ?? 4),
            'x11_forwarding' => filter_var($input['x11_forwarding'] ?? false, FILTER_VALIDATE_BOOL),
        ];
        return $this->recordGuarded('ssh.configure', (array) $this->agent->call('ssh.configure', $s, 90), $s);
    }

    /** @return list<array<string, mixed>> administrators and their SSH keys */
    public function sshKeys(): array
    {
        return array_values(array_map(static fn ($a): array => (array) json_decode((string) json_encode($a), true), (array) (((array) $this->agent->call('ssh.keys'))['admins'] ?? [])));
    }

    /**
     * Adds a public key for an administrator (root or a sudo/wheel member).
     * Grants a root-capable login: administrators only, with a recently
     * confirmed password (step-up on the route).
     *
     * @param array<string, mixed> $actor
     */
    public function addSshKey(string $user, string $key, array $actor): string
    {
        self::administrator($actor);
        $res = (array) $this->agent->call('ssh.key_add', ['user' => $user, 'key' => trim($key)], 60);
        $k = (array) ($res['key'] ?? []);
        $this->audit->record('ssh.key_add', 'server', 'ssh', AuditLogger::SUCCESS, ['user' => $user, 'fingerprint' => $k['fingerprint'] ?? '', 'type' => $k['type'] ?? '', 'comment' => $k['comment'] ?? '']);
        return sprintf('Key %s added for %s. Test a new SSH connection with it before turning off password logins.', (string) ($k['fingerprint'] ?? ''), $user);
    }

    /**
     * Generates a key pair in the panel and allows its public key for an
     * administrator. The private key is returned once, to be shown to the
     * administrator and downloaded; it is never stored, logged or sent to
     * the agent. RSA 4096 (PKCS#8 PEM, AES-256 encrypted with the
     * passphrase when one is given): OpenSSH, Windows' OpenSSH and
     * PuTTYgen read it; OpenSSH cannot read PEM Ed25519 keys.
     *
     * @param array<string, mixed> $actor
     * @return array{private: string, public: string, fingerprint: string, user: string, encrypted: bool}
     */
    public function generateSshKey(string $user, string $comment, string $passphrase, string $confirmation, array $actor): array
    {
        self::administrator($actor);
        if ($passphrase !== $confirmation) {
            throw new UserFacingException('The passphrases do not match.');
        }
        if ($passphrase !== '' && (mb_strlen($passphrase) < 8 || strlen($passphrase) > 1024)) {
            throw new UserFacingException('Use a passphrase of at least 8 characters, or leave it empty.');
        }
        $comment = trim((string) preg_replace('/[^A-Za-z0-9@._+-]+/', '-', $comment), '-');
        if ($comment === '') {
            $comment = $user . '@whacp';
        }
        $comment = substr($comment, 0, 64);
        $key = openssl_pkey_new(['private_key_type' => OPENSSL_KEYTYPE_RSA, 'private_key_bits' => 4096]);
        $details = $key === false ? false : openssl_pkey_get_details($key);
        $pem = '';
        $exported = $key !== false && ($passphrase === ''
            ? openssl_pkey_export($key, $pem)
            : openssl_pkey_export($key, $pem, $passphrase, ['encrypt_key_cipher' => OPENSSL_CIPHER_AES_256_CBC]));
        if ($details === false || !$exported || !isset($details['rsa']['n'], $details['rsa']['e'])) {
            throw new UserFacingException('The key pair could not be generated (OpenSSL).');
        }
        $public = 'ssh-rsa ' . base64_encode(self::sshString('ssh-rsa') . self::mpint($details['rsa']['e']) . self::mpint($details['rsa']['n'])) . ' ' . $comment;
        $res = (array) $this->agent->call('ssh.key_add', ['user' => $user, 'key' => $public], 60);
        $fp = (string) (((array) ($res['key'] ?? []))['fingerprint'] ?? '');
        $this->audit->record('ssh.key_generate', 'server', 'ssh', AuditLogger::SUCCESS, ['user' => $user, 'fingerprint' => $fp, 'type' => 'ssh-rsa', 'comment' => $comment, 'passphrase' => $passphrase !== '']);
        return ['private' => $pem, 'public' => $public, 'fingerprint' => $fp, 'user' => $user, 'encrypted' => $passphrase !== ''];
    }

    private static function sshString(string $s): string
    {
        return pack('N', strlen($s)) . $s;
    }

    private static function mpint(string $bigEndian): string
    {
        $b = ltrim($bigEndian, "\0");
        if ($b !== '' && (ord($b[0]) & 0x80)) {
            $b = "\0" . $b;
        }
        return self::sshString($b);
    }

    /** @param array<string, mixed> $actor */
    public function removeSshKey(string $user, string $fingerprint, array $actor): string
    {
        self::administrator($actor);
        $this->agent->call('ssh.key_remove', ['user' => $user, 'fingerprint' => $fingerprint], 60);
        $this->audit->record('ssh.key_remove', 'server', 'ssh', AuditLogger::SUCCESS, ['user' => $user, 'fingerprint' => $fingerprint]);
        return 'Key removed from ' . $user . '.';
    }

    /** @param array<string, mixed> $actor */
    private static function administrator(array $actor): void
    {
        if (($actor['role_name'] ?? '') !== 'admin') {
            throw new UserFacingException('Only administrators manage the server\'s SSH keys.');
        }
    }

    public function confirm(string $id): void
    {
        $this->agent->call('security.confirm', ['id' => $id]);
        $this->audit->record('security.confirm', 'server', 'security', AuditLogger::SUCCESS, ['pending' => $id]);
    }

    public function undo(string $id): void
    {
        $this->agent->call('security.undo', ['id' => $id]);
        $this->audit->record('security.undo', 'server', 'security', AuditLogger::SUCCESS, ['pending' => $id]);
    }

    /** @param array<string, mixed> $input */
    public function configureFail2ban(array $input): void
    {
        $ignore = preg_split('/[\s,]+/', trim((string) ($input['ignore'] ?? ''))) ?: [];
        $cfg = [
            'jails' => array_values(array_map('strval', (array) ($input['jails'] ?? []))),
            'ban_minutes' => (int) ($input['ban_minutes'] ?? 60),
            'find_minutes' => (int) ($input['find_minutes'] ?? 10),
            'max_retry' => (int) ($input['max_retry'] ?? 5),
            'ignore' => array_values(array_filter($ignore, static fn ($x) => $x !== '')),
        ];
        $this->agent->call('fail2ban.configure', $cfg, 60);
        $this->audit->record('fail2ban.configure', 'server', 'fail2ban', AuditLogger::SUCCESS, $cfg);
    }

    public function unban(string $jail, string $ip): void
    {
        if (filter_var($ip, FILTER_VALIDATE_IP) === false) {
            throw new UserFacingException('That is not an IP address.');
        }
        $this->agent->call('fail2ban.unban', ['jail' => $jail, 'ip' => $ip]);
        $this->audit->record('fail2ban.unban', 'server', 'fail2ban', AuditLogger::SUCCESS, ['jail' => $jail, 'ip' => $ip]);
    }
}
