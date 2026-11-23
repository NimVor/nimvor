<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class ApiReferenceTest extends SiteTestCase
{
    private const RAW = 'https://raw.githubusercontent.com/NimVor/nimvor/main/panel/resources/api/openapi.json';

    /** @return array<string, mixed> */
    private static function spec(): array
    {
        $ok = static fn (string $d, mixed $data, mixed $example): array => ['description' => $d, 'content' => ['application/json' => [
            'schema' => ['type' => 'object', 'properties' => ['success' => ['const' => true], 'data' => $data]],
            'example' => ['success' => true, 'data' => $example],
        ]]];
        return [
            'openapi' => '3.1.0',
            'info' => ['title' => 'NimVor panel API', 'version' => '1', 'description' => "Authenticate with a **token**.\n\n<script>alert(1)</script>"],
            'tags' => [['name' => 'Websites', 'description' => 'Create and delete websites.'], ['name' => 'Jobs', 'description' => 'Background jobs.']],
            'paths' => [
                '/websites/{id}' => [
                    'delete' => [
                        'operationId' => 'deleteWebsite', 'tags' => ['Websites'], 'summary' => 'Delete a website', 'description' => 'Files are **kept** unless asked.',
                        'x-permission' => 'websites.manage',
                        'parameters' => [['$ref' => '#/components/parameters/Id'], ['name' => 'remove_files', 'in' => 'query', 'description' => 'Also delete files.', 'schema' => ['type' => 'string', 'enum' => ['0', '1'], 'default' => '0']]],
                        'responses' => ['200' => $ok('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer', 'description' => 'The id.']]], ['deleted' => 7]), '404' => ['$ref' => '#/components/responses/NotFound']],
                    ],
                ],
                '/websites' => [
                    'post' => [
                        'operationId' => 'createWebsite', 'tags' => ['Websites'], 'summary' => 'Create a website', 'x-permission' => 'websites.manage',
                        'requestBody' => ['required' => true, 'content' => ['application/json' => [
                            'schema' => ['type' => 'object', 'required' => ['domain'], 'properties' => ['domain' => ['type' => 'string', 'description' => 'The host name.'], 'aliases' => ['type' => 'array', 'items' => ['type' => 'string']], 'cfg' => ['type' => 'object', 'properties' => ['mode' => ['type' => 'string', 'enum' => ['a', 'b']]]]]],
                            'example' => ['domain' => 'shop.example.com'],
                        ]]],
                        'responses' => ['201' => $ok('Created.', ['$ref' => '#/components/schemas/Website'], ['id' => 7])],
                    ],
                ],
                '/jobs/{id}' => ['get' => ['operationId' => 'getJob', 'tags' => ['Jobs'], 'summary' => 'A job', 'parameters' => [['name' => 'id', 'in' => 'path', 'required' => true, 'schema' => ['type' => 'string']]], 'responses' => ['200' => $ok('The job.', ['type' => 'object'], ['done' => true])]]],
            ],
            'components' => [
                'parameters' => ['Id' => ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The numeric id.', 'schema' => ['type' => 'integer']]],
                'schemas' => ['Website' => ['type' => 'object', 'properties' => ['id' => ['type' => 'integer', 'description' => 'The id.'], 'https' => ['type' => 'boolean']]]],
                'responses' => ['NotFound' => ['description' => 'It does not exist.', 'content' => ['application/json' => ['example' => ['success' => false, 'error' => ['code' => 'not_found', 'message' => 'Not found.']]]]]],
            ],
        ];
    }

    private function github(?array $spec = null): void
    {
        $this->http->on(self::RAW, 200, (string) json_encode($spec ?? self::spec()));
    }

    public function testOverviewListsGroupsAndErrors(): void
    {
        $this->github();
        $res = $this->get('/api-reference');
        self::assertSame(200, $res->status);
        $b = $res->body;
        self::assertStringContainsString('NimVor panel API', $b);
        self::assertStringContainsString('3 endpoints in 2 groups', $b);
        self::assertStringContainsString('href="/api-reference/websites"', $b);
        self::assertStringContainsString('href="/api-reference/jobs"', $b);
        self::assertStringContainsString('<strong>token</strong>', $b, 'the description is rendered as Markdown');
        self::assertStringNotContainsString('<script>alert', $b, 'raw HTML in the description is shown as text');
        self::assertStringContainsString('NotFound', $b);
        self::assertStringContainsString('curl -s -H', $b);
        self::assertStringContainsString('class="doc-side"', $b);
        self::assertStringContainsString('API reference', $this->get('/')->body, 'linked from every page');
    }

    public function testGroupPageShowsParametersBodiesResponsesAndExamples(): void
    {
        $this->github();
        $b = $this->get('/api-reference/websites')->body;
        self::assertStringContainsString('id="op-deleteWebsite"', $b);
        self::assertStringContainsString('<span class="api-m m-delete">DELETE</span>', $b);
        self::assertStringContainsString('<code>/websites/{id}</code>', $b);
        self::assertStringContainsString('<strong>kept</strong>', $b, 'operation description as Markdown');
        self::assertStringContainsString('<code>websites.manage</code>', $b, 'permission');
        self::assertStringContainsString('Needs a read/write token', $b);
        // Parameters: the shared one is resolved, enum and default are shown.
        self::assertStringContainsString('The numeric id.', $b);
        self::assertStringContainsString('one of 0, 1; default 0', $b);
        // Request body table, nested field and example.
        self::assertStringContainsString('<code>domain</code> <span class="api-req">required</span>', $b);
        self::assertStringContainsString('<code>cfg.mode</code>', $b);
        self::assertStringContainsString('<code>aliases</code>', $b);
        self::assertStringContainsString('string[]', $b);
        self::assertStringContainsString('&quot;domain&quot;: &quot;shop.example.com&quot;', $b);
        // Responses: a schema behind $ref, the example, and the shared error.
        self::assertStringContainsString('<span class="api-status s-2">201</span>', $b);
        self::assertStringContainsString('<code>https</code>', $b);
        self::assertStringContainsString('<span class="api-status s-4">404</span> It does not exist.', $b);
        self::assertStringContainsString('href="/api-reference#errors"', $b);
        // The curl example has the path filled in and the body.
        self::assertStringContainsString('curl -s -X POST', $b);
        self::assertStringContainsString('&quot;https://panel.example.com/api/v1/websites&quot;', str_replace('"', '&quot;', $b));
        self::assertStringContainsString('/api/v1/websites/7', $b);
        // Side menu lists this group's endpoints.
        self::assertStringContainsString('href="#op-createWebsite"', $b);
        self::assertSame(404, $this->get('/api-reference/nope')->status);
    }

    public function testSearchFindsEndpoints(): void
    {
        $this->github();
        $b = $this->get('/api-reference/search?q=manage')->body;
        self::assertStringContainsString('2 endpoints match', $b);
        self::assertStringContainsString('href="/api-reference/websites#op-createWebsite"', $b);
        self::assertStringContainsString('No endpoint matches', $this->get('/api-reference/search?q=zzzzqq')->body);
        self::assertStringContainsString('at least two characters', $this->get('/api-reference/search?q=a')->body);
        self::assertStringNotContainsString('<script>x', $this->get('/api-reference/search?q=' . rawurlencode('<script>x</script>'))->body);
    }

    public function testStoredCopySurvivesGitHubBeingDown(): void
    {
        $this->github();
        self::assertSame(200, $this->get('/api-reference')->status);
        // Later GitHub fails: the page still works from what is stored.
        $this->app->cache->delete('apispec:backoff');
        $this->app->db->run("UPDATE site_state SET updated_at = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 2 DAY) WHERE k = 'api:spec'");
        $this->http->on(self::RAW, 500, 'oops');
        self::assertSame(200, $this->get('/api-reference/websites')->status);
    }

    public function testNothingStoredAndGitHubDown(): void
    {
        $this->http->on(self::RAW, 500, 'oops');
        $res = $this->get('/api-reference');
        self::assertSame(503, $res->status);
        self::assertStringContainsString('not available right now', $res->body);
    }

    public function testAFileThatIsNotAnApiDescriptionIsNotStored(): void
    {
        $this->http->on(self::RAW, 200, '{"hello": "world"}');
        self::assertSame(503, $this->get('/api-reference')->status);
        self::assertNull($this->app->state->get('api:spec'));
    }

    public function testSitemapListsTheGroups(): void
    {
        $this->github();
        $this->get('/api-reference');
        $xml = $this->get('/sitemap.xml')->body;
        self::assertStringContainsString('https://nimvor.test/api-reference</loc>', $xml);
        self::assertStringContainsString('https://nimvor.test/api-reference/websites</loc>', $xml);
    }

    /** The real description in this repository renders on every page. */
    public function testTheRealDescriptionRenders(): void
    {
        $file = dirname(__DIR__, 3) . '/panel/resources/api/openapi.json';
        if (!is_file($file)) {
            self::markTestSkipped('the panel is not next to the website');
        }
        $spec = (array) json_decode((string) file_get_contents($file), true);
        $this->github($spec);
        self::assertSame(200, $this->get('/api-reference')->status);
        $ops = 0;
        foreach ($spec['tags'] as $t) {
            $slug = trim((string) preg_replace('/[^a-z0-9]+/', '-', strtolower($t['name'])), '-');
            $res = $this->get('/api-reference/' . $slug);
            self::assertSame(200, $res->status, $t['name']);
            $ops += substr_count($res->body, 'class="api-op"');
        }
        $expected = 0;
        foreach ($spec['paths'] as $item) {
            $expected += count(array_intersect(array_keys($item), ['get', 'post', 'put', 'patch', 'delete']));
        }
        self::assertSame($expected, $ops, 'every operation is shown exactly once');
    }
}
