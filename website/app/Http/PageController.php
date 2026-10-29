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
    private const INSTALL = 'curl -fsSL https://nimvor.com/install | sudo bash';

    public function home(Ctx $ctx): Response
    {
        return $ctx->render('pages/home', ['install' => self::INSTALL, 'posts' => array_slice($this->posts(), 0, 3), 'servers' => $this->app->stats->headline()]);
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
        return $ctx->render('pages/features');
    }

    public function resources(Ctx $ctx): Response
    {
        return $ctx->render('pages/resources');
    }

    public function documentation(Ctx $ctx): Response
    {
        return $ctx->render('pages/documentation', ['install' => self::INSTALL]);
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
        if ($v->fails()) {
            return $this->invalid($ctx, 'pages/build-services', $v->errors(), $extra);
        }
        $this->app->requests->create($ctx->user() === null ? null : (int) $ctx->user()['id'], $v->value('name'), $v->value('email'), $v->value('service'), $v->value('details'));
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'Thank you. We have your request and will reply by email.');
        return $ctx->redirect($ctx->user() === null ? '/resources/build-services' : '/account/requests');
    }

    public function sponsors(Ctx $ctx): Response
    {
        $list = $this->app->cache->get('sponsors:list');
        if (!is_array($list)) {
            $list = $this->app->sponsors->all();
            $this->app->cache->set('sponsors:list', $list, 300);
        }
        return $ctx->render('pages/sponsors', ['sponsors' => $list]);
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
        if ($v->fails()) {
            return $this->invalid($ctx, 'pages/contact', $v->errors());
        }
        $this->app->messages->create($ctx->user() === null ? null : (int) $ctx->user()['id'], $v->value('name'), $v->value('email'), $v->value('subject'), $v->value('message'));
        $this->app->cache->delete('report:overview');
        return $this->thanks($ctx, '/support/contact');
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
        $paths = ['/', '/features', '/resources', '/resources/documentation', '/resources/blog', '/resources/community', '/resources/build-services', '/resources/sponsors', '/resources/problems', '/resources/stats', '/docs', '/resources/about', '/support', '/support/contact', '/support/privacy'];
        foreach ($this->posts() as $p) {
            $paths[] = '/resources/blog/' . $p['slug'];
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
