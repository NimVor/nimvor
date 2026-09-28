<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Support\UserFacingException;

/**
 * HTTPS for WhaCP websites.
 *
 * Flow: obtain/validate the certificate on the agent → switch the website
 * to HTTPS through WebsiteService (which regenerates the Nginx config and
 * only reloads after nginx -t) → on any failure the website stays as it was.
 */
final class SslService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly WebsiteService $sites,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @param array<string, mixed> $site */
    public function issue(array $site, string $email, bool $includeAliases, bool $staging): void
    {
        if (!$site['enabled']) {
            throw new UserFacingException('Enable the website first.', 'Let\'s Encrypt must reach it over HTTP to verify the domain.');
        }
        if ($email !== '' && !filter_var($email, FILTER_VALIDATE_EMAIL)) {
            throw new UserFacingException('Enter a valid email address for expiry notices, or leave it empty.');
        }
        $names = array_merge([$site['domain']], $includeAliases ? $site['aliases'] : []);
        try {
            $res = (array) $this->agent->call('ssl.issue', ['cert_name' => $site['domain'], 'names' => $names, 'email' => $email, 'staging' => $staging], 320);
        } catch (AgentException $e) {
            $this->audit->record('ssl.issue', 'website', (string) $site['id'], AuditLogger::FAILURE, ['domain' => $site['domain'], 'error' => $e->getMessage()]);
            throw $e;
        }
        $this->sites->patch($site, [
            'https' => true,
            'force_https' => true,
            'ssl_mode' => 'letsencrypt',
            'cert_file' => (string) $res['cert_file'],
            'key_file' => (string) $res['key_file'],
        ], 'ssl.enable');
        $this->audit->record('ssl.issue', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'names' => $names, 'staging' => $staging]);
    }

    /** @param array<string, mixed> $site */
    public function installCustom(array $site, string $cert, string $key, string $chain): void
    {
        if (trim($cert) === '' || trim($key) === '') {
            throw new UserFacingException('Paste both the certificate and its private key.');
        }
        $res = (array) $this->agent->call('ssl.custom', ['name' => $site['domain'], 'certificate' => $cert, 'private_key' => $key, 'chain' => $chain, 'domain' => $site['domain']]);
        $this->sites->patch($site, [
            'https' => true,
            'ssl_mode' => 'custom',
            'cert_file' => (string) $res['cert_file'],
            'key_file' => (string) $res['key_file'],
        ], 'ssl.custom');
    }

    /**
     * Install a certificate for a key made on the server (the CA's
     * certificate for its CSR, or its self-signed certificate).
     *
     * @param array<string, mixed> $site
     */
    public function installFromKey(array $site, string $keyId, string $cert, string $chain, bool $selfSigned): void
    {
        if (!$selfSigned && trim($cert) === '') {
            throw new UserFacingException('Paste the certificate the certificate authority issued for this key.');
        }
        try {
            $res = (array) $this->agent->call('ssl.custom', ['name' => $site['domain'], 'key_id' => $keyId, 'certificate' => $selfSigned ? '' : $cert, 'chain' => $selfSigned ? '' : $chain, 'use_self_signed' => $selfSigned, 'domain' => $site['domain']]);
        } catch (AgentException $e) {
            $this->audit->record('ssl.install_key', 'website', (string) $site['id'], AuditLogger::FAILURE, ['domain' => $site['domain'], 'key_id' => $keyId, 'error' => $e->getMessage()]);
            throw $e;
        }
        $this->sites->patch($site, [
            'https' => true,
            'ssl_mode' => 'custom',
            'cert_file' => (string) $res['cert_file'],
            'key_file' => (string) $res['key_file'],
        ], 'ssl.custom');
        $this->audit->record('ssl.install_key', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'key_id' => $keyId, 'self_signed' => $selfSigned]);
    }

    /** @param array<string, mixed> $site */
    public function renew(array $site): void
    {
        if ($site['ssl_mode'] !== 'letsencrypt') {
            throw new UserFacingException('Only Let\'s Encrypt certificates can be renewed automatically. Upload a new certificate instead.');
        }
        $this->agent->call('ssl.renew', ['cert_name' => $site['domain']], 320);
        $this->audit->record('ssl.renew', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']]);
    }

    /** @param array<string, mixed> $site */
    public function disable(array $site, bool $deleteCertificate): void
    {
        $mode = $site['ssl_mode'];
        $this->sites->patch($site, ['https' => false, 'force_https' => false, 'hsts' => false, 'ssl_mode' => 'none', 'cert_file' => '', 'key_file' => ''], 'ssl.disable');
        if ($deleteCertificate && $mode === 'letsencrypt') {
            $this->agent->call('ssl.delete', ['cert_name' => $site['domain']]);
        }
    }

    /** @param array<string, mixed> $site */
    public function options(array $site, bool $force, bool $hsts): void
    {
        if (!$site['https']) {
            throw new UserFacingException('Enable HTTPS first.');
        }
        $this->sites->patch($site, ['force_https' => $force, 'hsts' => $hsts && $force], 'ssl.options');
    }

    /**
     * @param array<string, mixed> $site
     * @return array<string, mixed>|null
     */
    public function certificate(array $site): ?array
    {
        if (!$site['https'] || $site['cert_file'] === '') {
            return null;
        }
        try {
            return (array) $this->agent->call('ssl.inspect', ['cert_file' => $site['cert_file'], 'key_file' => $site['key_file']]);
        } catch (AgentException) {
            return null;
        }
    }

    /** @return array<string, mixed>|null */
    public function renewalStatus(): ?array
    {
        try {
            return (array) $this->agent->call('ssl.renewal_status');
        } catch (AgentException) {
            return null;
        }
    }
}
