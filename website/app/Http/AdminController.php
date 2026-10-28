<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Repositories\BuildRequests;
use NimVorSite\Repositories\Messages;
use NimVorSite\Repositories\Sponsors;
use NimVorSite\Support\Text;
use NimVorSite\Support\Validator;

/** Administrators only (the route table enforces it); every change is a POST with a CSRF token. */
final class AdminController extends Controller
{
    public function overview(Ctx $ctx): Response
    {
        $r = $this->app->reports->overview();
        return $ctx->render('admin/overview', ['report' => $r, 'recentMessages' => array_slice($this->app->messages->all(), 0, 5), 'recentRequests' => array_slice($this->app->requests->all(), 0, 5)]);
    }

    public function reports(Ctx $ctx): Response
    {
        return $ctx->render('admin/reports', ['report' => $this->app->reports->overview()]);
    }

    /** @param array<string, string> $params */
    public function csv(Ctx $ctx, array $params): Response
    {
        if (!in_array($params['type'] ?? '', ['users', 'requests', 'messages'], true)) {
            return $this->app->error($ctx, 404);
        }
        $fh = fopen('php://temp', 'r+');
        foreach ($this->app->reports->csv($params['type']) as $row) {
            fputcsv($fh, $row, ',', '"', '\\');
        }
        rewind($fh);
        $body = (string) stream_get_contents($fh);
        fclose($fh);
        return Response::text($body, 'text/csv')->header('Content-Disposition', 'attachment; filename="nimvor-' . $params['type'] . '-' . gmdate('Y-m-d') . '.csv"');
    }

    public function users(Ctx $ctx): Response
    {
        return $ctx->render('admin/users', ['users' => $this->app->users->page(200, 0)]);
    }

    /** @param array<string, string> $params */
    public function userActive(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        $target = $id === null ? null : $this->app->users->find($id);
        if ($target === null) {
            return $this->app->error($ctx, 404);
        }
        $activate = $ctx->req->input('active') === '1';
        if (!$activate && ((int) $target['id'] === (int) $ctx->user()['id'] || ($target['role'] === 'admin' && $this->app->users->admins() <= 1))) {
            $ctx->flash('error', 'You cannot deactivate yourself or the last administrator.');
            return $ctx->redirect('/admin/users');
        }
        $this->app->users->setActive((int) $target['id'], $activate);
        $ctx->flash('success', $target['email'] . ($activate ? ' was activated.' : ' was deactivated.'));
        return $ctx->redirect('/admin/users');
    }

    public function messages(Ctx $ctx): Response
    {
        return $ctx->render('admin/messages', ['messages' => $this->app->messages->all(), 'statuses' => Messages::STATUSES]);
    }

    /** @param array<string, string> $params */
    public function messageStatus(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        if ($id === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->messages->setStatus($id, $ctx->req->input('status'));
        $this->app->cache->delete('report:overview');
        return $ctx->redirect('/admin/messages');
    }

    public function requests(Ctx $ctx): Response
    {
        return $ctx->render('admin/requests', ['requests' => $this->app->requests->all(), 'statuses' => BuildRequests::STATUSES, 'services' => BuildRequests::SERVICES]);
    }

    /** @param array<string, string> $params */
    public function requestStatus(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        if ($id === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->requests->setStatus($id, $ctx->req->input('status'));
        $this->app->cache->delete('report:overview');
        return $ctx->redirect('/admin/requests');
    }

    public function posts(Ctx $ctx): Response
    {
        return $ctx->render('admin/posts', ['posts' => $this->app->posts->all()]);
    }

    /** @param array<string, string> $params */
    public function postForm(Ctx $ctx, array $params = []): Response
    {
        if (isset($params['id'])) {
            $id = $this->intParam($params, 'id');
            $post = $id === null ? null : $this->app->posts->find($id);
            if ($post === null) {
                return $this->app->error($ctx, 404);
            }
            return $ctx->render('admin/post-form', ['post' => $post, 'old' => $post]);
        }
        return $ctx->render('admin/post-form', ['post' => null, 'old' => ['status' => 'draft']]);
    }

    /** @param array<string, string> $params */
    public function postSave(Ctx $ctx, array $params = []): Response
    {
        $id = isset($params['id']) ? $this->intParam($params, 'id') : null;
        $existing = $id === null ? null : $this->app->posts->find($id);
        if (isset($params['id']) && $existing === null) {
            return $this->app->error($ctx, 404);
        }
        $v = new Validator($ctx->req->post);
        $v->text('title', 3, 200, 'Title')->text('body', 20, 100000, 'Text')->in('status', ['draft', 'published'], 'Choose draft or published.');
        if (mb_strlen($v->value('excerpt')) > 400) {
            $v->fail('excerpt', 'The summary can have at most 400 characters.');
        }
        $slug = Text::slug($v->value('slug') !== '' ? $v->value('slug') : $v->value('title'));
        if ($this->app->posts->slugTaken($slug, $id)) {
            $v->fail('slug', 'Another post already uses this address. Change it.');
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'admin/post-form', $v->errors(), ['post' => $existing]);
        }
        $this->app->posts->save($id, $slug, $v->value('title'), $v->value('excerpt'), $v->value('body'), $v->value('status'), (int) $ctx->user()['id']);
        $this->app->cache->delete('blog:list');
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'The post was saved.');
        return $ctx->redirect('/admin/posts');
    }

    /** @param array<string, string> $params */
    public function postDelete(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        if ($id === null || $this->app->posts->find($id) === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->posts->delete($id);
        $this->app->cache->delete('blog:list');
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'The post was deleted.');
        return $ctx->redirect('/admin/posts');
    }

    public function sponsors(Ctx $ctx): Response
    {
        return $ctx->render('admin/sponsors', ['sponsors' => $this->app->sponsors->all(), 'tiers' => Sponsors::TIERS]);
    }

    public function sponsorAdd(Ctx $ctx): Response
    {
        $v = new Validator($ctx->req->post);
        $v->text('name', 1, 120, 'name')->in('tier', Sponsors::TIERS, 'Choose a tier.');
        $url = $v->value('url');
        if (preg_match('#^https://[^\s<>"\']{3,240}$#', $url) !== 1) {
            $v->fail('url', 'Enter a link that starts with https://.');
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'admin/sponsors', $v->errors(), ['sponsors' => $this->app->sponsors->all(), 'tiers' => Sponsors::TIERS]);
        }
        $this->app->sponsors->create($v->value('name'), $url, $v->value('tier'));
        $this->app->cache->delete('sponsors:list');
        $this->app->cache->delete('report:overview');
        $ctx->flash('success', 'The sponsor was added.');
        return $ctx->redirect('/admin/sponsors');
    }

    /** @param array<string, string> $params */
    public function sponsorDelete(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        if ($id === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->sponsors->delete($id);
        $this->app->cache->delete('sponsors:list');
        $this->app->cache->delete('report:overview');
        return $ctx->redirect('/admin/sponsors');
    }
}
