<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Repositories\BuildRequests;
use NimVorSite\Support\Text;
use NimVorSite\Support\Validator;

final class PageController extends Controller
{
    private const INSTALL_SCRIPT = 'https://raw.githubusercontent.com/NimVor/nimvor/main/installer/bootstrap.sh';

    /**
     * The one-line installer: `curl -fsSL https://nimvor.com/install | sudo bash`. The script itself lives in the
     * repository (installer/bootstrap.sh); this sends curl to it. The address is fixed here, never taken from the
     * request. It is a temporary (302) redirect so it can move without clients remembering the old place.
     */
    public function installScript(Ctx $ctx): Response
    {
        return new Response(302, '', [
            'Location' => self::INSTALL_SCRIPT,
            'Cache-Control' => 'public, max-age=300',
        ]);
    }

    public function home(Ctx $ctx): Response
    {
        return $ctx->render('pages/home', ['posts' => array_slice($this->posts(), 0, 3), 'servers' => $this->app->stats->headline()]);
    }

    public function releases(Ctx $ctx): Response
    {
        $history = $this->app->releases->history();
        $series = (string) $ctx->req->input('series');
        $known = [];
        foreach ($history['releases'] as $r) {
            $known[(string) preg_replace('/^(\d+\.\d+).*$/', '$1', $r['version'])] = true;
        }
        return $ctx->render('pages/releases', [
            'history' => $history,
            'series' => isset($known[$series]) ? $series : '',
            'seriesList' => array_keys($known),
            'inline' => fn (string $md): string => $this->app->releases->inline($md),
            'brief' => fn (string $md): string => $this->app->releases->brief($md),
            'repo' => $this->app->github->repoUrl(),
        ]);
    }

    public function release(Ctx $ctx, array $params): Response
    {
        $history = $this->app->releases->history();
        $list = $history['releases'];
        $at = null;
        foreach ($list as $i => $r) {
            if ($r['version'] === ($params['version'] ?? '')) {
                $at = $i;
                break;
            }
        }
        if ($at === null) {
            return $this->app->error($ctx, 404);
        }
        return $ctx->render('pages/release', [
            'r' => $list[$at],
            'newer' => $list[$at - 1] ?? null,
            'older' => $list[$at + 1] ?? null,
            'inline' => fn (string $md): string => $this->app->releases->inline($md),
            'repo' => $this->app->github->repoUrl(),
        ]);
    }

    public function problems(Ctx $ctx): Response
    {
        $status = $ctx->req->input('status');
        return $ctx->render('pages/problems', [
            'board' => $this->app->problems->board(),
            'status' => in_array($status, ['reported', 'progress', 'resolved'], true) ? $status : '',
            'repo' => $this->app->github->repoUrl(),
        ]);
    }

    public function stats(Ctx $ctx): Response
    {
        return $ctx->render('pages/stats', ['stats' => $this->app->stats->public()]);
    }

    public function features(Ctx $ctx): Response
    {
        $latest = null;
        foreach ($this->app->releases->history()['releases'] as $r) {
            if ($r['kind'] !== 'pre') {
                $latest = $r;
                break;
            }
        }
        return $ctx->render('pages/features', ['latest' => $latest, 'inline' => fn (string $md): string => $this->app->releases->inline($md)]);
    }

    public function resources(Ctx $ctx): Response
    {
        return $ctx->render('pages/resources');
    }

    public function documentation(Ctx $ctx): Response
    {
        return $ctx->render('pages/documentation');
    }

    public function getStarted(Ctx $ctx): Response
    {
        return $ctx->render('pages/get-started', []);
    }

    public function blog(Ctx $ctx): Response
    {
        return $ctx->render('pages/blog', ['posts' => $this->posts()]);
    }

    /** @param array<string, string> $params */
    public function post(Ctx $ctx, array $params): Response
    {
        $post = $this->app->posts->bySlug($params['slug'] ?? '');
        if ($post === null) {
            $this->app->blogImporter->auto();
            $post = $this->app->posts->bySlug($params['slug'] ?? '');
        }
        if ($post === null) {
            return $this->app->error($ctx, 404);
        }
        return $ctx->render('pages/post', ['post' => $post, 'bodyHtml' => Text::html((string) $post['body'])]);
    }

    public function community(Ctx $ctx): Response
    {
        return $ctx->render('pages/community');
    }

    public function buildServices(Ctx $ctx): Response
    {
        return $ctx->render('pages/build-services', ['services' => BuildRequests::SERVICES, 'old' => $this->prefill($ctx)]);
    }

    public function buildServicesSubmit(Ctx $ctx): Response
    {
        $v = new Validator($ctx->req->post);
        $v->text('name', 1, 120, 'name')->email()->in('service', array_keys(BuildRequests::SERVICES), 'Choose a service.')->text('details', 20, 5000, 'Details');
        $extra = ['services' => BuildRequests::SERVICES];
        if ($v->value('website') !== '') {
            return $this->thanks($ctx, '/resources/build-services'); // a bot filled the hidden field
        }
        if ($this->app->limiter->hit('form:ip:' . $ctx->ip(), 6, 3600)) {
            return $this->app->error($ctx, 429);
        }
        if (($bad = $this->captchaError($ctx)) !== null) {
            $v->fail('captcha', $bad);
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'pages/build-services', $v->errors(), $extra);
        }
        $this->app->requests->create($ctx->user() === null ? null : (int) $ctx->user()['id'], $v->value('name'), $v->value('email'), $v->value('service'), $v->value('details'));
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'Thank you. We have your request and will reply by email.');
        return $ctx->redirect($ctx->user() === null ? '/resources/build-services' : '/account/requests');
    }

    public function videos(Ctx $ctx): Response
    {
        $list = $this->app->cache->get('videos:list');
        if (!is_array($list)) {
            $list = $this->app->videos->published();
            $this->app->cache->set('videos:list', $list, 300);
        }
        $groups = [];
        foreach ($list as $row) {
            $groups[(string) $row['category']][] = $row;
        }
        return $ctx->render('pages/videos', ['groups' => $groups, 'total' => count($list)]);
    }

    /** @param array<string, string> $params */
    public function video(Ctx $ctx, array $params): Response
    {
        $video = $this->app->videos->bySlug($params['slug'] ?? '');
        if ($video === null) {
            return $this->app->error($ctx, 404);
        }
        $more = array_values(array_filter($this->app->videos->published(), static fn (array $r): bool => $r['id'] !== $video['id']));
        usort($more, static fn (array $a, array $b): int => [$a['category'] !== $video['category'], $a['sort_order']] <=> [$b['category'] !== $video['category'], $b['sort_order']]);
        return $ctx->render('pages/video', ['video' => $video, 'more' => array_slice($more, 0, 4)]);
    }

    /**
     * An uploaded video or cover picture. Only files that belong to a video are served, an unpublished
     * video's only to an administrator, and a browser can ask for part of a file (seeking).
     *
     * @param array<string, string> $params
     */
    public function videoFile(Ctx $ctx, array $params): Response
    {
        $name = $params['file'] ?? '';
        $path = $this->app->videoStore->path($name);
        $row = $path === null ? null : $this->app->videos->byFile($name);
        if ($path === null || $row === null || ((int) $row['published'] !== 1 && !$ctx->auth->isAdmin())) {
            return $this->app->error($ctx, 404);
        }
        $types = ['mp4' => 'video/mp4', 'webm' => 'video/webm', 'jpg' => 'image/jpeg', 'png' => 'image/png', 'webp' => 'image/webp'];
        $unpublished = (int) $row['published'] !== 1;
        return Response::file($path, $types[pathinfo($name, PATHINFO_EXTENSION)], $ctx->req->server['HTTP_RANGE'] ?? '', $unpublished ? 'private, no-store' : 'public, max-age=86400');
    }

    public function sponsors(Ctx $ctx): Response
    {
        $list = $this->app->cache->get('sponsors:list');
        if (!is_array($list)) {
            $d = $this->app->sponsorDisplay();
            $list = $this->app->sponsors->shown(array_keys(array_filter(['github' => $d['github'], 'patreon' => $d['patreon'], 'manual' => $d['manual']])));
            $this->app->cache->set('sponsors:list', $list, 300);
        }
        return $ctx->render('pages/sponsors', ['sponsors' => $list, 'display' => $this->app->sponsorDisplay()]);
    }

    /** The funding link in the repository (nimvor.com/sponsor) lands on the Sponsors page. */
    public function sponsorRedirect(Ctx $ctx): Response
    {
        return Response::redirect('/resources/sponsors', 301);
    }

    public function about(Ctx $ctx): Response
    {
        return $ctx->render('pages/about');
    }

    public function support(Ctx $ctx): Response
    {
        return $ctx->render('pages/support');
    }

    public function contact(Ctx $ctx): Response
    {
        return $ctx->render('pages/contact', ['old' => $this->prefill($ctx)]);
    }

    public function contactSubmit(Ctx $ctx): Response
    {
        $v = new Validator($ctx->req->post);
        $v->text('name', 1, 120, 'name')->email()->text('subject', 3, 200, 'Subject')->text('message', 10, 5000, 'Message');
        if ($v->value('website') !== '') {
            return $this->thanks($ctx, '/support/contact');
        }
        if ($this->app->limiter->hit('form:ip:' . $ctx->ip(), 6, 3600)) {
            return $this->app->error($ctx, 429);
        }
        if (($bad = $this->captchaError($ctx)) !== null) {
            $v->fail('captcha', $bad);
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'pages/contact', $v->errors());
        }
        $category = in_array($v->value('category'), \NimVorSite\Repositories\Tickets::CATEGORIES, true) ? $v->value('category') : 'general';
        $t = $this->app->ticketService->open($ctx->user() === null ? null : (int) $ctx->user()['id'], $v->value('name'), $v->value('email'), $v->value('subject'), $v->value('message'), $category);
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'Thank you. Your ticket number is ' . $t['ref'] . '. Keep this page\'s address: it is the private link to follow your ticket and answer us.');
        return $ctx->redirect('/support/tickets/' . $t['ref'] . '/' . $t['token']);
    }

    /** The old Messages pages became Tickets. */
    public function legacyMessages(Ctx $ctx): Response
    {
        return Response::redirect(str_starts_with($ctx->req->path, '/admin') ? '/admin/tickets' : '/account/tickets', 301);
    }

    public function privacy(Ctx $ctx): Response
    {
        return $ctx->render('pages/privacy');
    }

    public function robots(Ctx $ctx): Response
    {
        return Response::text("User-agent: *\nDisallow: /admin\nDisallow: /account\nDisallow: /login\nDisallow: /register\nSitemap: " . $this->app->config['url'] . "/sitemap.xml\n");
    }

    public function sitemap(Ctx $ctx): Response
    {
        $base = (string) $this->app->config['url'];
        $paths = ['/', '/features', '/get-started', '/resources', '/resources/documentation', '/resources/blog', '/resources/community', '/resources/build-services', '/resources/sponsors', '/resources/videos', '/resources/releases', '/resources/problems', '/resources/stats', '/docs', '/resources/about', '/support', '/support/contact', '/support/privacy'];
        foreach ($this->posts() as $p) {
            $paths[] = '/resources/blog/' . $p['slug'];
        }
        foreach ($this->app->videos->published() as $vid) {
            $paths[] = '/resources/videos/' . $vid['slug'];
        }
        foreach ($this->app->releases->history()['releases'] as $rel) {
            $paths[] = '/resources/releases/' . $rel['version'];
        }
        foreach ($this->app->docs->menu() as $d) {
            if ($d['slug'] !== 'index') {
                $paths[] = '/docs/' . $d['slug'];
            }
        }
        $xml = '<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">';
        foreach ($paths as $p) {
            $xml .= '<url><loc>' . e($base . $p) . '</loc></url>';
        }
        return Response::text($xml . '</urlset>', 'application/xml');
    }

    public function health(Ctx $ctx): Response
    {
        try {
            $this->app->db->scalar('SELECT 1');
            $this->app->cache->set('health', 1, 10);
            return Response::text("ok\n")->header('Cache-Control', 'no-store');
        } catch (\Throwable $e) {
            error_log('nimvor-site health: ' . $e->getMessage());
            return Response::text("unavailable\n", 'text/plain', 503)->header('Cache-Control', 'no-store');
        }
    }

    /** @return list<array<string, mixed>> */
    private function posts(): array
    {
        $posts = $this->app->cache->get('blog:list');
        if (!is_array($posts)) {
            $this->app->blogImporter->auto();
            $posts = $this->app->posts->published();
            $this->app->cache->set('blog:list', $posts, 120);
        }
        return $posts;
    }

    /** @return array<string, string> */
    private function prefill(Ctx $ctx): array
    {
        $u = $ctx->user();
        return $u === null ? [] : ['name' => (string) $u['name'], 'email' => (string) $u['email']];
    }

    private function thanks(Ctx $ctx, string $to): Response
    {
        $ctx->flash('success', 'Thank you. Your message was sent and we will reply by email.');
        return $ctx->redirect($to);
    }
}
