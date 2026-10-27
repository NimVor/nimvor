<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\NotificationService;
use WhaCP\View\View;

/**
 * The notifications page, the top bar's unread count and read state.
 */
final class NotificationsController extends Controller
{
    private const PER_PAGE = 50;

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly NotificationService $notifications,
        private readonly Connection $db,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        $filter = $this->filter($request);
        $page = max(1, (int) $request->query('page', 1));
        $total = $this->notifications->count($user, $filter);
        [$cond, $params] = \WhaCP\Hosting\Scope::condition($user);
        return $this->render('notifications/index', [
            'rows' => $this->notifications->list($user, $filter, self::PER_PAGE, ($page - 1) * self::PER_PAGE),
            'total' => $total,
            'unread' => $this->notifications->unreadCount($user),
            'page' => $page,
            'pages' => max(1, (int) ceil($total / self::PER_PAGE)),
            'filter' => $filter,
            'websites' => $this->db->fetchAll("SELECT id, domain FROM websites WHERE {$cond} ORDER BY domain", $params),
            'emailOn' => (int) $this->db->scalar('SELECT notify_email FROM users WHERE id = ?', [(int) $user['id']]) === 1,
        ]);
    }

    /** Unread count and the latest unread items (top bar). */
    public function summary(Request $request): Response
    {
        $user = $this->user($request);
        $latest = array_map(static fn (array $n): array => [
            'id' => (int) $n['id'], 'severity' => $n['severity'], 'title' => $n['title'], 'created_at' => $n['created_at'],
        ], $this->notifications->list($user, ['unread' => true], 5));
        // The bell's list: the newest unread ones (the page lists them all).
        $items = array_map(static function (array $n): array {
            $link = (string) $n['link'];
            return [
                'id' => (int) $n['id'], 'severity' => (string) $n['severity'], 'title' => (string) $n['title'],
                'body' => mb_strimwidth((string) $n['body'], 0, 160, '…'), 'ago' => time_ago((string) $n['created_at']),
                'read' => (bool) $n['is_read'],
                // Only links inside the panel.
                'link' => str_starts_with($link, '/') && !str_starts_with($link, '//') ? $link : '',
                'where' => (string) ($n['domain'] ?? ''),
            ];
        }, $this->notifications->list($user, ['unread' => true], 8));
        return Response::json(['success' => true, 'unread' => $this->notifications->unreadCount($user), 'latest' => $latest, 'items' => $items]);
    }

    public function read(Request $request): Response
    {
        $user = $this->user($request);
        $ids = $request->input('all') === '1' ? null : array_map('intval', (array) $request->input('ids', []));
        $n = $this->notifications->markRead($user, $ids);
        if ($request->wantsJson()) {
            return Response::json(['success' => true, 'marked' => $n, 'unread' => $this->notifications->unreadCount($user)]);
        }
        return Response::redirect('/notifications' . $this->backQuery($request));
    }

    /** Email me about failures (opt-in). */
    public function email(Request $request): Response
    {
        $on = $request->input('notify_email') === '1';
        $this->db->run('UPDATE users SET notify_email = ? WHERE id = ?', [$on ? 1 : 0, $this->userId($request)]);
        return $this->redirectWithSuccess('/notifications', $on
            ? 'You will be emailed about failures you can see. Email must be set up under Settings.'
            : 'You will no longer be emailed about failures.');
    }

    /** @return array{unread?: bool, severity?: string, website_id?: int} */
    private function filter(Request $request): array
    {
        $f = [];
        if ($request->query('show') !== 'all') {
            $f['unread'] = true;
        }
        $sev = (string) $request->query('severity', '');
        if (in_array($sev, NotificationService::SEVERITIES, true)) {
            $f['severity'] = $sev;
        }
        if ((int) $request->query('website', 0) > 0) {
            $f['website_id'] = (int) $request->query('website');
        }
        return $f;
    }

    private function backQuery(Request $request): string
    {
        $q = array_filter([
            'show' => $request->input('show') === 'all' ? 'all' : '',
            'severity' => in_array($request->input('severity'), NotificationService::SEVERITIES, true) ? (string) $request->input('severity') : '',
            'website' => (int) $request->input('website', 0) > 0 ? (string) (int) $request->input('website') : '',
        ]);
        return $q === [] ? '' : '?' . http_build_query($q);
    }
}
