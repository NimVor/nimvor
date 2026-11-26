<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Database\Connection;
use NimVor\Support\UserFacingException;

/**
 * Mail names on their own: an email domain gets mail.<domain> and webmail.<domain>, with a certificate of their own,
 * as soon as both names point at this server.
 *
 * Both names point here when the domain's DNS zone is hosted on this panel and delegated to it (the A records are
 * added to the zone by this service), or when the DNS the domain really uses says so; this is the same test AutoSSL
 * uses for websites. The scheduler looks again every few minutes, for up to WINDOW_DAYS from when the domain was added
 * (or from when this feature first saw it, for existing domains). It never asks Let's Encrypt before the names
 * resolve, so a domain whose DNS is elsewhere costs a few DNS lookups, not failed certificate requests.
 *
 * An administrator who turned a domain's mail names off ('off') is never overruled, and one who turned them on by
 * hand ('done') is left alone. A domain that did not get them within the window ('gave_up'), or after MAX_TRIES
 * refused certificate requests, is left for the Mail names card's button; the card says what happened.
 */
final class MailNamesAuto
{
    public const WINDOW_DAYS = 7;
    public const MAX_TRIES = 4;
    private const PER_PASS = 10;
    private const LOOK_AGAIN = 300;
    private const AFTER_REQUEST = 600;
    private const AFTER_REFUSAL = 1800;
    /** An administrator's rights, for adding the two records to a zone from the scheduler. */
    private const SYSTEM = ['id' => 0, 'role_scope' => 'all'];

    public function __construct(
        private readonly Connection $db,
        private readonly MailService $mail,
        private readonly AutoSslService $ssl,
        private readonly DnsService $dns,
        private readonly SettingsService $settings,
        private readonly ServerContext $server,
    ) {
    }

    public function enabled(): bool
    {
        return $this->settings->bool('mail_names_auto');
    }

    private function save(int $id, string $state, int $tries, ?int $nextIn, string $note): void
    {
        $this->db->run(
            'UPDATE mail_domains SET names_auto = ?, names_auto_tries = ?, names_auto_next = ?, names_auto_note = ? WHERE id = ?',
            [$state, $tries, $nextIn === null ? null : gmdate('c', time() + $nextIn), mb_substr($note, 0, 300), $id]
        );
    }

    /** An administrator turned the names off: the job leaves the domain alone from now on. */
    public function turnedOff(int $id): void
    {
        $this->save($id, 'off', 0, null, 'Turned off by an administrator; they stay off until an administrator turns them on.');
    }

    /** An administrator turned the names on by hand. */
    public function turnedOn(int $id): void
    {
        $this->save($id, 'done', 0, null, 'Turned on by an administrator.');
    }

    /**
     * A new email domain: when its zone is hosted here, add the two A records now, and let the next scheduler pass
     * turn the names on once the DNS answers. Never fails the caller.
     *
     * @param array<string, mixed> $domain
     */
    public function prepare(array $domain): void
    {
        if (!$this->enabled()) {
            return;
        }
        try {
            $this->mail->publishNamesDns($domain, self::SYSTEM);
        } catch (\Throwable) {
            // The scheduler tries again.
        }
    }

    /**
     * One look at one domain. Returns what was done, or '' when there was nothing to say.
     *
     * @param array<string, mixed> $d a mail_domains row
     */
    public function attempt(array $d): string
    {
        $id = (int) $d['id'];
        $name = (string) $d['domain'];
        if (in_array((string) $d['names_auto'], ['off', 'done', 'gave_up'], true)) {
            return '';
        }
        $status = $this->mail->namesStatus($d);
        if ($status === []) {
            return ''; // the agent cannot answer now: look again later, nothing counted
        }
        $tries = (int) $d['names_auto_tries'];
        if (!empty($status['enabled']) && !empty($status['cert_ready'])) {
            $this->save($id, 'done', $tries, null, 'The mail names are on.');
            return $name . ': mail names are on';
        }
        if (!empty($status['enabled'])) {
            $this->save($id, 'enabling', $tries, self::AFTER_REQUEST, 'Waiting for the certificate.');
            return '';
        }
        $since = strtotime((string) ($d['names_auto_since'] ?? '')) ?: strtotime((string) $d['created_at']) ?: time();
        if (time() > $since + self::WINDOW_DAYS * 86400) {
            $this->save($id, 'gave_up', $tries, null, 'The names did not point to this server within ' . self::WINDOW_DAYS . ' days, so they were not turned on by themselves. Point ' . 'mail.' . $name . ' and webmail.' . $name . ' here, then turn them on below.');
            return $name . ': mail names not turned on automatically (DNS never pointed here)';
        }
        $ip = $this->dns->serverIpv4();
        if ($ip === null) {
            $this->save($id, 'pending', $tries, self::LOOK_AGAIN, 'This server\'s address is not known yet.');
            return '';
        }
        // A zone hosted here gets the two records; a name that already points somewhere is left as it is.
        try {
            $this->mail->publishNamesDns($d, self::SYSTEM);
        } catch (\Throwable) {
        }
        if (!$this->ssl->pointsHere('mail.' . $name, $ip) || !$this->ssl->pointsHere('webmail.' . $name, $ip)) {
            $until = gmdate('M j', $since + self::WINDOW_DAYS * 86400);
            $this->save($id, 'pending', $tries, self::LOOK_AGAIN, 'Waiting for mail.' . $name . ' and webmail.' . $name . ' to point to this server (' . $ip . '); looked at every few minutes until ' . $until . '.');
            return '';
        }
        try {
            $this->mail->enableNames($d, $this->email($d));
        } catch (UserFacingException $e) {
            $tries++;
            if ($tries >= self::MAX_TRIES) {
                $this->save($id, 'gave_up', $tries, null, 'The certificate was refused ' . $tries . ' times: ' . $e->getMessage() . ' Fix that, then turn the names on below.');
                return $name . ': mail names not turned on automatically (' . $e->getMessage() . ')';
            }
            $this->save($id, 'pending', $tries, self::AFTER_REFUSAL, 'The certificate request failed (' . $e->getMessage() . '); trying again.');
            return $name . ': certificate request failed (' . $e->getMessage() . ')';
        }
        $this->save($id, 'enabling', $tries, self::AFTER_REQUEST, 'Getting the certificate.');
        return $name . ': asked for the certificate of mail.' . $name . ' and webmail.' . $name;
    }

    /**
     * The scheduler's pass: a few domains that are due, oldest look first.
     *
     * @return list<string>
     */
    public function run(): array
    {
        if (!$this->enabled()) {
            return [];
        }
        $due = $this->db->fetchAll(
            "SELECT * FROM mail_domains WHERE server_id = ? AND names_auto IN ('pending', 'enabling') AND (names_auto_next IS NULL OR names_auto_next <= ?) ORDER BY COALESCE(names_auto_next, ''), id LIMIT " . self::PER_PASS,
            [$this->server->id(), gmdate('c')]
        );
        $out = [];
        foreach ($due as $d) {
            try {
                $line = $this->attempt($d);
            } catch (\Throwable $e) {
                $this->save((int) $d['id'], (string) $d['names_auto'], (int) $d['names_auto_tries'], self::LOOK_AGAIN, 'Not checked: ' . $e->getMessage());
                $line = '';
            }
            if ($line !== '') {
                $out[] = 'mail names: ' . $line;
            }
        }
        return $out;
    }

    /** The address for certificate notices: the domain's owner, else an administrator. */
    private function email(array $d): string
    {
        $email = $d['owner_id'] !== null ? $this->db->scalar('SELECT email FROM users WHERE id = ?', [(int) $d['owner_id']]) : null;
        $email ??= $this->db->scalar("SELECT u.email FROM users u JOIN roles r ON r.id = u.role_id WHERE r.name = 'admin' AND u.is_active = 1 ORDER BY u.id LIMIT 1");
        return is_string($email) && filter_var($email, FILTER_VALIDATE_EMAIL) ? $email : '';
    }
}
