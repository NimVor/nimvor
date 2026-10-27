<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Core\Config;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\ServerContext;
use WhaCP\View\View;

/**
 * Centralized log viewer. Website logs are visible to anyone with access to
 * the website; server logs only to roles with server-wide scope.
 */
final class LogsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly Config $config,
        private readonly \WhaCP\Database\Connection $db,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        if (!Scope::isAll($user)) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Server logs are only available to administrators. Open a website to see its logs.', 'status' => 403], 403);
        }
        $sources = [];
        $error = null;
        try {
            $sources = (array) $this->agent->call('logs.sources');
        } catch (AgentException $e) {
            $error = $e;
        }
        $sources[] = ['id' => 'panel', 'label' => 'NimVor panel log', 'group' => 'NimVor', 'kind' => 'file'];
        // Logs → <kind> in the sidebar shows only that kind of log.
        $view = (string) $request->query('view', '');
        if (isset(self::CATEGORIES[$view])) {
            $sources = array_values(array_filter($sources, static fn (array $s): bool => self::category((string) $s['id']) === $view));
        }
        return $this->render('logs/index', ['sources' => $sources, 'agentError' => $error, 'site' => null, 'initialSource' => (string) $request->query('source', ''), 'category' => self::CATEGORIES[$view] ?? null]);
    }

    /** The kinds of server logs (Logs' sub-menu): view => title. */
    public const CATEGORIES = [
        'web' => 'Web server logs', 'php' => 'PHP-FPM logs', 'mail' => 'Mail server logs', 'database' => 'Database server logs',
        'security' => 'Security logs', 'ssl' => 'SSL and Let\'s Encrypt logs', 'system' => 'System logs', 'services' => 'Service logs', 'whacp' => 'NimVor logs',
    ];

    /** Which kind a log source belongs to. */
    public static function category(string $id): string
    {
        $unit = str_starts_with($id, 'journal:') ? substr($id, 8) : '';
        return match (true) {
            str_starts_with($id, 'nginx:') || $unit === 'nginx.service' => 'web',
            str_starts_with($id, 'php:') || (bool) preg_match('/^php.*fpm/', $unit) => 'php',
            $id === 'system:mail' || (bool) preg_match('/^(postfix|dovecot|rspamd|whacp-opendkim|whacp-greylist|whacp-mailrate)/', $unit) => 'mail',
            $id === 'system:mysql' || (bool) preg_match('/^(mariadb|mysql)/', $unit) => 'database',
            in_array($id, ['system:auth', 'system:fail2ban', 'system:ufw'], true) || (bool) preg_match('/^(fail2ban|sshd?|ssh)\./', $unit) => 'security',
            $id === 'system:letsencrypt' || $unit === 'certbot.service' => 'ssl',
            $id === 'system:syslog' => 'system',
            $id === 'panel' || $unit === 'whacp-agent.service' || str_starts_with($unit, 'whacp-panel') => 'whacp',
            default => 'services',
        };
    }

    public function site(Request $request, string $id): Response
    {
        $site = $this->sites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $sources = [];
        $error = null;
        try {
            $sources = (array) $this->agent->call('logs.sources', ['domain' => $site['domain']]);
        } catch (AgentException $e) {
            $error = $e;
        }
        foreach ($this->db->fetchAll('SELECT name, slug FROM workers WHERE website_id = ? ORDER BY id', [(int) $site['id']]) as $w) {
            $sources[] = ['id' => 'journal:whacp-worker-' . $w['slug'] . '.service', 'label' => 'Worker: ' . $w['name'], 'group' => 'Workers', 'kind' => 'journal'];
        }
        return $this->render('logs/site', ['sources' => $sources, 'agentError' => $error, 'site' => $site, 'tab' => 'logs', 'initialSource' => (string) $request->query('source', '')]);
    }

    private function allowed(Request $request, string $source): bool
    {
        $user = $this->user($request);
        if (preg_match('/^site:([a-z0-9.-]+):/', $source, $m)) {
            if (Scope::isAll($user)) {
                return true;
            }
            $site = $this->sites->findByDomain($this->server->id(), $m[1]);
            return $site !== null && $this->sites->find($this->server->id(), (int) $site['id'], $user) !== null;
        }
        if (preg_match('/^journal:whacp-worker-([a-z0-9-]+)\.service$/', $source, $m)) {
            if (Scope::isAll($user)) {
                return true;
            }
            $siteId = $this->db->scalar('SELECT website_id FROM workers WHERE server_id = ? AND slug = ?', [$this->server->id(), $m[1]]);
            return $siteId !== null && $this->sites->find($this->server->id(), (int) $siteId, $user) !== null;
        }
        return Scope::isAll($user);
    }

    public function tail(Request $request): Response
    {
        $source = (string) $request->query('source', '');
        if (!$this->allowed($request, $source)) {
            return Response::json(['success' => false, 'error' => ['message' => 'You may not read this log.']], 403);
        }
        $lines = max(10, min(5000, (int) $request->query('lines', 200)));
        $filter = mb_substr((string) $request->query('filter', ''), 0, 200);
        if ($source === 'panel') {
            return Response::json(['success' => true, 'data' => $this->panelTail($lines, $filter, (string) $request->query('follow') === '1', (int) $request->query('offset', 0))]);
        }
        try {
            $data = $this->agent->call('logs.tail', [
                'source' => $source, 'lines' => $lines, 'filter' => $filter,
                'follow' => (string) $request->query('follow') === '1',
                'offset' => (int) $request->query('offset', 0),
                'cursor' => (string) $request->query('cursor', ''),
            ]);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 502);
        }
        return Response::json(['success' => true, 'data' => $data]);
    }

    /** @return array<string, mixed> */
    private function panelTail(int $n, string $filter, bool $follow, int $offset): array
    {
        $file = $this->config->string('log.file');
        $size = is_file($file) ? (int) filesize($file) : 0;
        if ($size === 0) {
            return ['lines' => [], 'offset' => 0];
        }
        $fh = fopen($file, 'rb');
        if ($follow && $offset >= 0 && $offset <= $size) {
            fseek($fh, $offset);
            $data = (string) fread($fh, 1 << 20);
            $cut = strrpos($data, "\n");
            $data = $cut === false ? '' : substr($data, 0, $cut + 1);
            $lines = $data === '' ? [] : explode("\n", rtrim($data, "\n"));
            fclose($fh);
            return ['lines' => $this->filter($lines, $filter), 'offset' => $offset + strlen($data)];
        }
        fseek($fh, max(0, $size - (4 << 20)));
        $lines = explode("\n", rtrim((string) stream_get_contents($fh), "\n"));
        fclose($fh);
        return ['lines' => array_slice($this->filter($lines, $filter), -$n), 'offset' => $size];
    }

    /**
     * @param list<string> $lines
     * @return list<string>
     */
    private function filter(array $lines, string $f): array
    {
        return $f === '' ? $lines : array_values(array_filter($lines, static fn (string $l): bool => stripos($l, $f) !== false));
    }

    public function download(Request $request): Response
    {
        $source = (string) $request->query('source', '');
        if (!$this->allowed($request, $source) || $source === 'panel') {
            return Response::html('Not found', 404);
        }
        try {
            $first = (array) $this->agent->call('logs.download', ['source' => $source, 'offset' => 0]);
        } catch (AgentException $e) {
            return Response::html('The log cannot be downloaded: ' . e($e->getMessage()), 404);
        }
        $agent = $this->agent;
        return Response::stream(static function () use ($first, $agent, $source): void {
            if (session_status() === PHP_SESSION_ACTIVE) {
                session_write_close();
            }
            $chunk = $first;
            $offset = 0;
            while (true) {
                $bytes = base64_decode((string) ($chunk['data'] ?? ''), true) ?: '';
                echo $bytes;
                flush();
                $offset += strlen($bytes);
                if (!empty($chunk['eof']) || $bytes === '') {
                    break;
                }
                $chunk = (array) $agent->call('logs.download', ['source' => $source, 'offset' => $offset]);
            }
        }, [
            'Content-Type' => 'text/plain; charset=utf-8',
            'Content-Disposition' => 'attachment; filename="' . (preg_replace('/[^A-Za-z0-9._-]/', '_', (string) ($first['name'] ?? 'log.txt')) ?: 'log.txt') . '"',
            'X-Content-Type-Options' => 'nosniff',
        ]);
    }
}
