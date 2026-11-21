<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Support\UserFacingException;

/**
 * The picture of a website's home page for its card: taken by a headless
 * browser in the agent (as the website's own account), kept there, and
 * served by the panel. Which websites have one is remembered here, so a
 * list of cards needs no agent call.
 */
final class ScreenshotService
{
    /** Seconds between two attempts for one website (a picture takes up to a minute of CPU). */
    public const MIN_INTERVAL = 30;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
    ) {
    }

    /** Whether a browser is installed for pictures. */
    public function installed(): bool
    {
        try {
            return !empty(((array) $this->agent->call('screenshot.status'))['installed']);
        } catch (AgentException) {
            return false;
        }
    }

    /** Starts installing the browser; returns the agent job id. */
    public function install(): string
    {
        return (string) ((array) $this->agent->call('screenshot.install'))['job_id'];
    }

    /**
     * When each website's picture was taken.
     *
     * @param list<int> $ids
     * @return array<int, string> website id => time
     */
    public function takenAt(array $ids): array
    {
        if ($ids === []) {
            return [];
        }
        $in = implode(',', array_fill(0, count($ids), '?'));
        $out = [];
        foreach ($this->db->fetchAll("SELECT website_id, taken_at FROM site_screenshots WHERE taken_at IS NOT NULL AND website_id IN ({$in})", $ids) as $r) {
            $out[(int) $r['website_id']] = (string) $r['taken_at'];
        }
        return $out;
    }

    /**
     * Takes a new picture.
     *
     * @param array<string, mixed> $site
     * @return string when it was taken
     */
    public function capture(array $site): string
    {
        $id = (int) $site['id'];
        $last = $this->db->fetch('SELECT attempted_at FROM site_screenshots WHERE website_id = ?', [$id]);
        if ($last !== null && strtotime((string) $last['attempted_at']) > time() - self::MIN_INTERVAL) {
            throw new UserFacingException('A picture was taken a moment ago. Try again in half a minute.');
        }
        $now = gmdate('c');
        $this->db->run('INSERT INTO site_screenshots (website_id, attempted_at) VALUES (?, ?) ON CONFLICT(website_id) DO UPDATE SET attempted_at = excluded.attempted_at', [$id, $now]);
        try {
            $res = (array) $this->agent->call('screenshot.capture', ['domain' => (string) $site['domain'], 'user' => (string) $site['system_user'], 'root' => (string) $site['root']]
                // A website with its own address is served only there.
                + (!empty($site['ipv4']) ? ['ipv4' => (string) $site['ipv4']] : []), 120);
        } catch (AgentException $e) {
            $this->db->run('UPDATE site_screenshots SET error = ? WHERE website_id = ?', [mb_substr($e->getMessage(), 0, 500), $id]);
            throw $e;
        }
        $at = (string) ($res['taken_at'] ?? $now);
        $this->db->run("UPDATE site_screenshots SET taken_at = ?, error = '' WHERE website_id = ?", [$at, $id]);
        return $at;
    }

    /**
     * The kept picture (JPEG bytes), or null.
     *
     * @param array<string, mixed> $site
     */
    public function image(array $site): ?string
    {
        $res = (array) $this->agent->call('screenshot.get', ['domain' => (string) $site['domain']]);
        if (empty($res['exists'])) {
            return null;
        }
        $bytes = base64_decode((string) ($res['jpeg'] ?? ''), true);
        return $bytes === false || $bytes === '' ? null : $bytes;
    }
}
