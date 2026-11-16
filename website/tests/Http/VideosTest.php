<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class VideosTest extends SiteTestCase
{
    private const YT = 'https://www.youtube.com/watch?v=dQw4w9WgXcQ';
    private string $tmp;

    protected function setUp(): void
    {
        parent::setUp();
        $this->tmp = sys_get_temp_dir() . '/nimvor-upload-' . bin2hex(random_bytes(4));
        mkdir($this->tmp);
    }

    protected function tearDown(): void
    {
        foreach (glob($this->tmp . '/*') ?: [] as $f) {
            @unlink($f);
        }
        @rmdir($this->tmp);
    }

    /** A file as PHP puts it in $_FILES. @return array<string, mixed> */
    private function upload(string $content, string $name = 'v.mp4'): array
    {
        $p = $this->tmp . '/' . bin2hex(random_bytes(4));
        file_put_contents($p, $content);
        return ['name' => $name, 'type' => 'application/octet-stream', 'tmp_name' => $p, 'error' => UPLOAD_ERR_OK, 'size' => strlen($content)];
    }

    private function mp4(int $bytes = 4096): string
    {
        return "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00" . str_repeat('A', $bytes);
    }

    /** @param array<string, string> $over */
    private function fields(array $over = []): array
    {
        return $over + ['title' => 'Backups and restore', 'description' => 'Set up backups.', 'category' => 'Operations', 'source' => 'youtube', 'youtube_url' => self::YT, 'duration' => '4:30', 'sort_order' => '10', 'slug' => '', 'published' => '1'];
    }

    public function testVideosAreForAdministratorsOnly(): void
    {
        $this->cookies = [];
        self::assertContains($this->get('/admin/videos')->status, [302, 303]);
        $this->loginAs('client', 'client@example.test');
        self::assertSame(403, $this->get('/admin/videos')->status);
        self::assertSame(403, $this->post('/admin/videos', $this->fields())->status);
    }

    public function testAYouTubeVideoIsAddedShownAndPlayedFromTheNoCookieHost(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        self::assertStringContainsString('No videos yet', $this->get('/resources/videos')->body);
        $res = $this->post('/admin/videos', $this->fields());
        self::assertSame([303, '/admin/videos'], [$res->status, $res->headers['Location']]);
        $list = $this->get('/resources/videos')->body;
        self::assertStringContainsString('Backups and restore', $list, 'the cached list was refreshed');
        self::assertStringContainsString('Operations', $list);
        $page = $this->get('/resources/videos/backups-and-restore');
        self::assertSame(200, $page->status);
        self::assertStringContainsString('https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0', $page->body);
        self::assertStringContainsString('frame-src https://www.youtube-nocookie.com', $page->headers['Content-Security-Policy']);
        self::assertStringContainsString('/resources/videos/backups-and-restore', $this->get('/sitemap.xml')->body);
        self::assertSame('dQw4w9WgXcQ', $this->row('SELECT youtube_id FROM videos')['youtube_id']);
    }

    public function testBadLinksAndFieldsAreRefused(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        foreach ([
            ['youtube_url' => 'https://evil.example/watch?v=dQw4w9WgXcQ'],
            ['youtube_url' => ''],
            ['source' => 'ftp'],
            ['title' => ''],
            ['duration' => 'long'],
            ['sort_order' => 'first'],
        ] as $bad) {
            self::assertSame(422, $this->post('/admin/videos', $this->fields($bad))->status, json_encode($bad));
        }
        self::assertSame(0, $this->rows('videos'));
        $this->post('/admin/videos', $this->fields());
        $dup = $this->post('/admin/videos', $this->fields());
        self::assertSame(422, $dup->status);
        self::assertStringContainsString('Another video already uses this address', $dup->body);
    }

    public function testAnUploadedVideoIsStoredOutsideTheWebRootAndServedWithRanges(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $mp4 = $this->mp4();
        $res = $this->post('/admin/videos', $this->fields(['source' => 'upload', 'youtube_url' => '']), true, ['video' => $this->upload($mp4)]);
        self::assertSame(303, $res->status, $res->body);
        $row = $this->row('SELECT * FROM videos');
        self::assertMatchesRegularExpression('/^[a-f0-9]{32}\.mp4$/', $row['file']);
        self::assertSame('video/mp4', $row['mime']);
        $file = $row['file'];

        $this->cookies = []; // a visitor
        $page = $this->get('/resources/videos/backups-and-restore');
        self::assertStringContainsString("/media/videos/{$file}", $page->body);
        self::assertStringContainsString('<video', $page->body);

        $full = $this->get("/media/videos/{$file}");
        self::assertSame([200, 'video/mp4', 'bytes', (string) strlen($mp4)], [$full->status, $full->headers['Content-Type'], $full->headers['Accept-Ranges'], $full->headers['Content-Length']]);
        self::assertStringContainsString('max-age=86400', $full->headers['Cache-Control']);
        $part = $this->get("/media/videos/{$file}", ['HTTP_RANGE' => 'bytes=10-19']);
        self::assertSame([206, 'bytes 10-19/' . strlen($mp4), '10'], [$part->status, $part->headers['Content-Range'], $part->headers['Content-Length']]);
        $tail = $this->get("/media/videos/{$file}", ['HTTP_RANGE' => 'bytes=-5']);
        self::assertSame('bytes ' . (strlen($mp4) - 5) . '-' . (strlen($mp4) - 1) . '/' . strlen($mp4), $tail->headers['Content-Range']);
        self::assertSame(416, $this->get("/media/videos/{$file}", ['HTTP_RANGE' => 'bytes=99999-'])->status);
        self::assertSame(416, $this->get("/media/videos/{$file}", ['HTTP_RANGE' => 'lines=1-2'])->status);
        self::assertSame(0, (int) $this->app->db->scalar("SELECT COUNT(*) FROM visits WHERE route LIKE '/media/%'"), 'file requests are not page visits');
    }

    public function testOnlyRealVideoAndPictureFilesAreAccepted(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $up = ['source' => 'upload', 'youtube_url' => ''];
        // A script renamed to .mp4, an empty file, a missing file, and one over the limit (1 MB in tests).
        foreach ([[$this->upload('<?php echo 1; ?>' . str_repeat(' ', 2000)), 'neither'], [$this->upload('x'), 'too small'], [$this->upload($this->mp4(1048576)), 'larger than 1 MB']] as [$f, $why]) {
            $r = $this->post('/admin/videos', $this->fields($up), true, ['video' => $f]);
            self::assertSame(422, $r->status, $why);
        }
        self::assertSame(422, $this->post('/admin/videos', $this->fields($up))->status, 'no file');
        $badPoster = $this->post('/admin/videos', $this->fields($up), true, ['video' => $this->upload($this->mp4()), 'poster' => $this->upload('GIF89a' . str_repeat('x', 100), 'p.gif')]);
        self::assertSame(422, $badPoster->status);
        self::assertSame(0, $this->rows('videos'));
        self::assertSame([], glob($this->app->config['videos']['dir'] . '/*') ?: [], 'a refused upload leaves no file behind');
    }

    public function testADraftIsHiddenFromVisitorsButNotFromAnAdministrator(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $this->post('/admin/videos', $this->fields(['source' => 'upload', 'published' => '0', 'youtube_url' => '']), true, ['video' => $this->upload($this->mp4()), 'poster' => $this->upload("\xFF\xD8\xFF\xE0" . str_repeat('j', 200), 'p.jpg')]);
        $row = $this->row('SELECT * FROM videos');
        self::assertSame(0, (int) $row['published']);
        self::assertMatchesRegularExpression('/\.jpg$/', $row['poster']);
        self::assertSame(200, $this->get("/media/videos/{$row['file']}")->status, 'an administrator can preview');
        self::assertStringContainsString('no-store', $this->get("/media/videos/{$row['file']}")->headers['Cache-Control']);
        $admin = $this->cookies;
        $this->cookies = [];
        self::assertSame(404, $this->get('/resources/videos/backups-and-restore')->status);
        self::assertSame(404, $this->get("/media/videos/{$row['file']}")->status);
        self::assertSame(404, $this->get("/media/videos/{$row['poster']}")->status);
        self::assertStringNotContainsString('Backups and restore', $this->get('/resources/videos')->body);
        $this->cookies = $admin;
        $this->post("/admin/videos/{$row['id']}/publish");
        $this->cookies = [];
        self::assertSame(200, $this->get('/resources/videos/backups-and-restore')->status);
        self::assertSame(200, $this->get("/media/videos/{$row['poster']}")->status);
    }

    public function testNothingButStoredFilesIsServed(): void
    {
        foreach (['../../config/config.php', '..%2F..%2Fconfig.php', 'x.mp4', 'index.php', str_repeat('a', 32) . '.mp4', str_repeat('a', 32) . '.php'] as $name) {
            self::assertSame(404, $this->get('/media/videos/' . $name)->status, $name);
        }
    }

    public function testEditingHidingAndDeletingRemovesTheFiles(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $this->post('/admin/videos', $this->fields(['source' => 'upload', 'youtube_url' => '']), true, ['video' => $this->upload($this->mp4()), 'poster' => $this->upload("\x89PNG\r\n\x1A\n" . str_repeat('p', 100), 'p.png')]);
        $row = $this->row('SELECT * FROM videos');
        $id = (int) $row['id'];
        self::assertStringContainsString('Backups and restore', $this->get("/admin/videos/{$id}/edit")->body);
        $edit = $this->post("/admin/videos/{$id}/edit", ['title' => 'Renamed', 'description' => '', 'category' => 'Operations', 'duration' => '', 'sort_order' => '5', 'slug' => 'backups-and-restore', 'published' => '1', 'remove_poster' => '1']);
        self::assertSame(303, $edit->status);
        $after = $this->row('SELECT * FROM videos');
        self::assertSame(['Renamed', null], [$after['title'], $after['poster']]);
        self::assertNull($this->app->videoStore->path($row['poster']), 'the removed cover picture is deleted');
        self::assertNotNull($this->app->videoStore->path($row['file']));
        $this->post("/admin/videos/{$id}/delete");
        self::assertSame(0, $this->rows('videos'));
        self::assertNull($this->app->videoStore->path($row['file']), 'the video file is deleted with the video');
        self::assertSame(404, $this->get('/admin/videos/999/edit')->status);
        self::assertSame(404, $this->post('/admin/videos/999/delete')->status);
    }
}
