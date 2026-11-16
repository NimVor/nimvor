<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Repositories\BuildRequests;
use NimVorSite\Repositories\Messages;
use NimVorSite\Repositories\Sponsors;
use NimVorSite\Services\GitHubException;
use NimVorSite\Support\Text;
use NimVorSite\Support\Validator;
use NimVorSite\Support\YouTube;

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
        return $ctx->render('admin/sponsors', ['sponsors' => $this->app->sponsors->all(), 'tiers' => Sponsors::TIERS, 'hasToken' => $this->app->github->hasToken()]);
    }

    /** Copies the public GitHub sponsors in; the ones added by hand stay. */
    public function sponsorSync(Ctx $ctx): Response
    {
        try {
            $r = $this->app->sponsorSync->sync();
            $ctx->flash('success', 'Synced with GitHub: ' . $r['total'] . ' public sponsor(s), ' . $r['removed'] . ' no longer sponsoring.');
        } catch (GitHubException $e) {
            $ctx->flash('error', $e->getMessage());
        }
        return $ctx->redirect('/admin/sponsors');
    }

    public function content(Ctx $ctx): Response
    {
        return $ctx->render('admin/content', [
            'pages' => $this->app->docs->menu(),
            'syncedAt' => $this->app->docsService->lastSync(),
            'stats' => $this->app->stats->public(),
            'repo' => $this->app->github->repoUrl(),
            'hasToken' => $this->app->github->hasToken(),
        ]);
    }

    public function docsSync(Ctx $ctx): Response
    {
        try {
            $r = $this->app->docsService->sync();
            $ctx->flash('success', 'Documentation synced from GitHub: ' . $r['total'] . ' page(s), ' . $r['changed'] . ' changed, ' . $r['removed'] . ' removed.');
        } catch (GitHubException $e) {
            $ctx->flash('error', $e->getMessage());
        }
        return $ctx->redirect('/admin/content');
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
            return $this->invalid($ctx, 'admin/sponsors', $v->errors(), ['sponsors' => $this->app->sponsors->all(), 'tiers' => Sponsors::TIERS, 'hasToken' => $this->app->github->hasToken()]);
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

    private function videoPage(Ctx $ctx): array
    {
        $all = $this->app->videos->all();
        return ['videos' => $all, 'categories' => array_values(array_unique(array_map(static fn (array $r): string => (string) $r['category'], $all))), 'maxMb' => intdiv($this->app->videoStore->maxVideoBytes(), 1048576), 'phpMax' => (string) ini_get('upload_max_filesize')];
    }

    public function videos(Ctx $ctx): Response
    {
        return $ctx->render('admin/videos', $this->videoPage($ctx));
    }

    /** Checks the fields both forms share; returns [slug, sortOrder]. */
    private function videoFields(Validator $v, ?int $id): array
    {
        $v->text('title', 1, 160, 'title')->text('category', 1, 40, 'category');
        if (mb_strlen($v->value('description')) > 2000) {
            $v->fail('description', 'The description can have at most 2000 characters.');
        }
        if ($v->value('duration') !== '' && preg_match('/^\d{1,2}:\d{2}(:\d{2})?$/', $v->value('duration')) !== 1) {
            $v->fail('duration', 'Write the length as 4:30 or 1:02:30, or leave it empty.');
        }
        $order = $v->value('sort_order') === '' ? 100 : (ctype_digit($v->value('sort_order')) && (int) $v->value('sort_order') <= 9999 ? (int) $v->value('sort_order') : -1);
        if ($order < 0) {
            $v->fail('sort_order', 'Use a whole number from 0 to 9999.');
        }
        $slug = Text::slug($v->value('slug') !== '' ? $v->value('slug') : $v->value('title'));
        if ($this->app->videos->slugTaken($slug, $id)) {
            $v->fail('slug', 'Another video already uses this address. Change it.');
        }
        return [$slug, $order];
    }

    private function videoChanged(): void
    {
        $this->app->cache->delete('videos:list');
        $this->app->cache->delete('report:overview');
    }

    public function videoAdd(Ctx $ctx): Response
    {
        $v = new Validator($ctx->req->post);
        $v->in('source', ['youtube', 'upload'], 'Choose YouTube or an uploaded file.');
        [$slug, $order] = $this->videoFields($v, null);
        $ytId = null;
        $files = $ctx->req->files;
        if ($v->value('source') === 'youtube') {
            $ytId = YouTube::id($v->value('youtube_url'));
            if ($ytId === null) {
                $v->fail('youtube_url', 'Paste a YouTube link, such as https://www.youtube.com/watch?v=… or https://youtu.be/…');
            }
        } elseif ($v->value('source') === 'upload' && (int) ($files['video']['error'] ?? UPLOAD_ERR_NO_FILE) === UPLOAD_ERR_NO_FILE) {
            $v->fail('video', 'Choose the video file to upload.');
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'admin/videos', $v->errors(), $this->videoPage($ctx));
        }
        $stored = null;
        $poster = null;
        try {
            if ($v->value('source') === 'upload') {
                $stored = $this->app->videoStore->saveVideo($files['video']);
                if ((int) ($files['poster']['error'] ?? UPLOAD_ERR_NO_FILE) !== UPLOAD_ERR_NO_FILE) {
                    $poster = $this->app->videoStore->savePoster($files['poster']);
                }
            }
        } catch (\RuntimeException $e) {
            $this->app->videoStore->delete($stored['name'] ?? null);
            return $this->invalid($ctx, 'admin/videos', ['video' => $e->getMessage()], $this->videoPage($ctx));
        }
        $this->app->videos->create([
            'slug' => $slug, 'title' => $v->value('title'), 'description' => $v->value('description'), 'category' => $v->value('category'),
            'source' => $v->value('source'), 'youtube_id' => $ytId, 'file' => $stored['name'] ?? null, 'mime' => $stored['mime'] ?? null,
            'size_bytes' => $stored['size'] ?? null, 'poster' => $poster, 'duration' => $v->value('duration'), 'sort_order' => $order,
            'published' => ($ctx->req->post['published'] ?? '') === '1' ? 1 : 0,
        ]);
        $this->videoChanged();
        $ctx->flash('success', 'The video was added' . (($ctx->req->post['published'] ?? '') === '1' ? ' and is on the Videos page.' : ' as a draft. Publish it when it is ready.'));
        return $ctx->redirect('/admin/videos');
    }

    /** @param array<string, string> $params */
    public function videoEditForm(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        $video = $id === null ? null : $this->app->videos->find($id);
        if ($video === null) {
            return $this->app->error($ctx, 404);
        }
        return $ctx->render('admin/video-edit', ['video' => $video, 'old' => $video, 'categories' => array_values(array_unique(array_map(static fn (array $r): string => (string) $r['category'], $this->app->videos->all())))]);
    }

    /** @param array<string, string> $params */
    public function videoEdit(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        $video = $id === null ? null : $this->app->videos->find($id);
        if ($video === null) {
            return $this->app->error($ctx, 404);
        }
        $v = new Validator($ctx->req->post);
        [$slug, $order] = $this->videoFields($v, $id);
        $files = $ctx->req->files;
        $newPoster = null;
        $oldPoster = $video['poster'];
        if (!$v->fails() && (int) ($files['poster']['error'] ?? UPLOAD_ERR_NO_FILE) !== UPLOAD_ERR_NO_FILE) {
            try {
                $newPoster = $this->app->videoStore->savePoster($files['poster']);
            } catch (\RuntimeException $e) {
                $v->fail('poster', $e->getMessage());
            }
        }
        if ($v->fails()) {
            return $this->invalid($ctx, 'admin/video-edit', $v->errors(), ['video' => $video, 'categories' => array_values(array_unique(array_map(static fn (array $r): string => (string) $r['category'], $this->app->videos->all())))]);
        }
        $this->app->videos->update($id, $slug, $v->value('title'), $v->value('description'), $v->value('category'), $v->value('duration'), $order, ($ctx->req->post['published'] ?? '') === '1');
        if ($newPoster !== null) {
            $this->app->videos->setPoster($id, $newPoster);
            $this->app->videoStore->delete($oldPoster);
        } elseif (($ctx->req->post['remove_poster'] ?? '') === '1' && $oldPoster !== null) {
            $this->app->videos->setPoster($id, null);
            $this->app->videoStore->delete($oldPoster);
        }
        $this->videoChanged();
        $ctx->flash('success', 'The video was saved.');
        return $ctx->redirect('/admin/videos');
    }

    /** @param array<string, string> $params */
    public function videoPublish(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        $video = $id === null ? null : $this->app->videos->find($id);
        if ($video === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->videos->setPublished($id, (int) $video['published'] !== 1);
        $this->videoChanged();
        $ctx->flash('success', (int) $video['published'] !== 1 ? 'The video is on the Videos page.' : 'The video is hidden from the Videos page.');
        return $ctx->redirect('/admin/videos');
    }

    /** @param array<string, string> $params */
    public function videoDelete(Ctx $ctx, array $params): Response
    {
        $id = $this->intParam($params, 'id');
        $video = $id === null ? null : $this->app->videos->find($id);
        if ($video === null) {
            return $this->app->error($ctx, 404);
        }
        $this->app->videos->delete($id);
        $this->app->videoStore->delete($video['file']);
        $this->app->videoStore->delete($video['poster']);
        $this->videoChanged();
        $ctx->flash('success', 'The video was deleted.');
        return $ctx->redirect('/admin/videos');
    }
}
