<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Support\UserFacingException;

/**
 * Outgoing mail limits: recipients per website account and hour, counted
 * and enforced by the agent's policy service at the mail server. Website
 * mail (PHP's mail(), cron jobs, deploy hooks) goes through NimVor's
 * sendmail to it; website accounts may not use Postfix's own sendmail
 * while the limits are on.
 *
 * alerts() (scheduler) turns the service's "limit reached" events into
 * notifications for the administrators and the website's owner.
 */
final class MailRateService
{
    public const MAX = 1000000;

    public function __construct(
        private readonly AgentClient $agent,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly WebsiteService $websites,
        private readonly ?NotificationService $notifications = null,
    ) {
    }

    /** @return array<string, mixed> */
    public function state(): array
    {
        return (array) json_decode((string) json_encode($this->agent->call('mail.rate_get', [], 60)), true);
    }

    /**
     * @param array<string, mixed> $input enabled, per_hour, accounts (name => limit or '')
     * @param list<string> $known website accounts
     * @return array{enabled: bool, per_hour: int, accounts: array<string, int>}
     */
    public static function validate(array $input, array $known): array
    {
        $errors = [];
        $per = filter_var($input['per_hour'] ?? '', FILTER_VALIDATE_INT);
        if ($per === false || $per < 0 || $per > self::MAX) {
            $errors['per_hour'] = 'Enter a number between 0 (no limit) and ' . self::MAX . '.';
            $per = 0;
        }
        $accounts = [];
        foreach ((array) ($input['accounts'] ?? []) as $name => $raw) {
            $name = (string) $name;
            if (trim((string) $raw) === '') {
                continue; // the default applies
            }
            $n = filter_var($raw, FILTER_VALIDATE_INT);
            if (!in_array($name, $known, true)) {
                $errors['accounts'] = $name . ' is not a website account.';
            } elseif ($n === false || $n < 0 || $n > self::MAX) {
                $errors['accounts'] = 'Enter a number between 0 (no limit) and ' . self::MAX . ' for ' . $name . '.';
            } else {
                $accounts[$name] = $n;
            }
        }
        if ($errors !== []) {
            throw new UserFacingException(implode(' ', $errors));
        }
        ksort($accounts);
        return ['enabled' => filter_var($input['enabled'] ?? false, FILTER_VALIDATE_BOOL), 'per_hour' => $per, 'accounts' => $accounts];
    }

    /**
     * Turning the limits on first re-applies every PHP website, so its
     * PHP uses NimVor's sendmail (websites saved before this version use
     * Postfix's, which website accounts may then no longer use). The
     * limits stay off when a website cannot be re-applied.
     *
     * @param array{enabled: bool, per_hour: int, accounts: array<string, int>} $cfg
     */
    public function save(array $cfg, bool $wasEnabled): void
    {
        if ($cfg['enabled'] && !$wasEnabled) {
            $failed = [];
            foreach ($this->sites->all($this->server->id(), ['role_scope' => 'all']) as $site) {
                if (($site['kind'] ?? '') !== 'php') {
                    continue;
                }
                try {
                    $this->websites->applyState($site);
                } catch (\Throwable $e) {
                    $failed[] = $site['domain'] . ' (' . $e->getMessage() . ')';
                }
            }
            if ($failed !== []) {
                throw new UserFacingException('The limits were not turned on: these websites could not be updated to send mail through NimVor: ' . implode('; ', $failed) . '. Fix them (Websites → the website → Save) and try again.');
            }
        }
        $this->agent->call('mail.rate_set', ['enabled' => $cfg['enabled'], 'per_hour' => $cfg['per_hour'], 'accounts' => (object) $cfg['accounts']], 180);
    }

    public function reset(string $account): void
    {
        $this->agent->call('mail.rate_reset', ['account' => $account], 60);
    }

    /** @return list<string> what was reported */
    public function alerts(): array
    {
        if ($this->notifications === null) {
            return [];
        }
        try {
            $state = $this->state();
        } catch (AgentException $e) {
            if (in_array($e->errorCode(), ['unknown_action'], true)) {
                return [];
            }
            throw $e;
        }
        if (empty($state['config']['enabled'])) {
            return [];
        }
        $out = [];
        $serverId = $this->server->id();
        foreach ((array) ($state['usage']['events'] ?? []) as $ev) {
            $time = (int) ($ev['time'] ?? 0);
            if ($time < time() - 86400) {
                continue;
            }
            $name = (string) ($ev['name'] ?? '');
            $uid = (int) ($ev['uid'] ?? 0);
            $limit = (int) ($ev['limit'] ?? 0);
            $who = $name !== '' ? $name : 'UID ' . $uid;
            $title = 'Mail sending limit reached: ' . $who;
            $body = "The website account {$who} sent mail to {$limit} recipients within the hour; more mail from it is deferred until the next hour (or until an administrator lets it send again). A sudden increase can mean a compromised website sending spam.";
            $sites = $name !== '' ? $this->sites->usingSystemUser($serverId, $name) : [];
            $key = 'mailrate:' . $serverId . ':' . $uid . ':' . $time;
            $new = $this->notifications->notify($key, 'mail.limit', 'warning', $title, ['body' => $body, 'link' => '/mail/admin/sending-limits']);
            if ($new !== null) {
                $out[] = $title;
                // The website's owner hears of it too.
                foreach ($sites as $s) {
                    if (!empty($s['owner_id'])) {
                        $this->notifications->notify($key . ':owner:' . (int) $s['owner_id'], 'mail.limit', 'warning', 'Your website reached its mail sending limit', ['body' => "Mail sent from your website to more than {$limit} recipients within an hour is deferred until the next hour. If you did not send this mail, your website may have been compromised: contact your hosting provider.", 'website_id' => (int) $s['id'], 'owner_id' => (int) $s['owner_id']]);
                        break;
                    }
                }
            }
        }
        return $out;
    }
}
