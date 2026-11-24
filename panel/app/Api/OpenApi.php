<?php

declare(strict_types=1);

namespace NimVor\Api;

/**
 * Builds the panel API's OpenAPI document from the PHP files in resources/api/spec.
 *
 * The built document is committed as resources/api/openapi.json (it is what the API
 * serves and what the website's API reference reads); bin/build-openapi writes it and
 * a test fails when it is stale or when a route has no operation. Every spec file
 * returns ['tags' => [...], 'paths' => [...], 'schemas' => [...]].
 */
final class OpenApi
{
    public const VERSION = '1';

    /** The statuses every operation can answer, added unless an operation lists them itself. */
    private const COMMON_ERRORS = [401 => 'Unauthorized', 403 => 'Forbidden', 429 => 'RateLimited'];

    /** @return array<string, mixed> */
    public static function build(?string $dir = null): array
    {
        $dir ??= dirname(__DIR__, 2) . '/resources/api/spec';
        $tags = $paths = $schemas = [];
        $files = glob($dir . '/*.php') ?: [];
        sort($files);
        foreach ($files as $file) {
            /** @var array{tags?: list<array<string, mixed>>, paths?: array<string, mixed>, schemas?: array<string, mixed>} $part */
            $part = require $file;
            $tags = array_merge($tags, $part['tags'] ?? []);
            // Spec files may add methods to a path another file started, but never redefine one.
            foreach ((array) ($part['paths'] ?? []) as $path => $methods) {
                foreach ((array) $methods as $method => $op) {
                    if (isset($paths[$path][$method])) {
                        throw new \LogicException("Duplicate operation: $method $path");
                    }
                    $paths[$path][$method] = $op;
                }
            }
            $schemas = array_merge($schemas, $part['schemas'] ?? []);
        }
        ksort($schemas);
        foreach ($paths as &$item) {
            foreach ($item as $method => &$op) {
                if (!is_array($op) || !in_array($method, ['get', 'post', 'put', 'patch', 'delete'], true)) {
                    continue;
                }
                foreach (self::COMMON_ERRORS as $status => $name) {
                    $op['responses'][(string) $status] ??= ['$ref' => '#/components/responses/' . $name];
                }
                ksort($op['responses']);
            }
            unset($op);
        }
        unset($item);

        return [
            'openapi' => '3.1.0',
            'info' => [
                'title' => 'NimVor panel API',
                'version' => self::VERSION,
                'description' => self::intro(),
                'license' => ['name' => 'MIT', 'url' => 'https://opensource.org/licenses/MIT'],
            ],
            'servers' => [['url' => '/api/v1', 'description' => 'Your panel, for example https://panel.example.com/api/v1']],
            'security' => [['bearer' => []]],
            'tags' => $tags,
            'paths' => $paths,
            'components' => [
                'securitySchemes' => ['bearer' => ['type' => 'http', 'scheme' => 'bearer', 'description' => 'A personal API token (Profile → API tokens): `Authorization: Bearer nimvor_…`.']],
                'parameters' => [
                    'Id' => ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The numeric id.', 'schema' => ['type' => 'integer', 'minimum' => 1]],
                    'Server' => ['name' => 'X-NimVor-Server', 'in' => 'header', 'required' => false, 'description' => 'The id of the managed server to act on. Default: the server the panel runs on.', 'schema' => ['type' => 'integer']],
                ],
                'schemas' => $schemas + ['Error' => self::errorSchema()],
                'responses' => self::errorResponses(),
            ],
        ];
    }

    /** @param array<string, mixed> $spec */
    public static function encode(array $spec): string
    {
        return json_encode($spec, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR) . "\n";
    }

    // ---- helpers the spec files use -------------------------------------------------

    /** @return array<string, string> */
    public static function ref(string $schema): array
    {
        return ['$ref' => '#/components/schemas/' . $schema];
    }

    /**
     * The `{"success": true, "data": …}` envelope around a data schema.
     *
     * @param array<string, mixed> $data
     * @return array<string, mixed>
     */
    public static function envelope(array $data): array
    {
        return ['type' => 'object', 'required' => ['success', 'data'], 'properties' => ['success' => ['const' => true], 'data' => $data]];
    }

    /**
     * A success response with an example of the whole body.
     *
     * @param array<string, mixed> $data schema of "data"
     * @return array<string, mixed>
     */
    public static function res(string $description, array $data, mixed $example): array
    {
        return ['description' => $description, 'content' => ['application/json' => [
            'schema' => self::envelope($data),
            'example' => ['success' => true, 'data' => $example],
        ]]];
    }

    /**
     * A JSON request body.
     *
     * @param array<string, mixed> $schema
     * @return array<string, mixed>
     */
    public static function body(array $schema, mixed $example, bool $required = true): array
    {
        return ['required' => $required, 'content' => ['application/json' => ['schema' => $schema, 'example' => $example]]];
    }

    /** @return array<string, string> */
    public static function err(string $name): array
    {
        return ['$ref' => '#/components/responses/' . $name];
    }

    /**
     * @param array<string, mixed> $extra
     * @return array<string, mixed>
     */
    public static function query(string $name, string $description, array $schema, array $extra = []): array
    {
        return ['name' => $name, 'in' => 'query', 'required' => false, 'description' => $description, 'schema' => $schema] + $extra;
    }

    // ---- the parts every operation shares -------------------------------------------

    private static function intro(): string
    {
        return <<<'MD'
The NimVor panel API manages websites, databases, backups, SSL certificates, deployments and more
over HTTPS and JSON. Every endpoint uses the same services as the web interface, so validation,
plan limits, ownership scope, auditing and the agent's safety checks are the same.

**Authentication.** Create a token under *Profile → API tokens* (it is shown once) and send it as
`Authorization: Bearer nimvor_…`. A token acts with its owner's permissions and ownership scope (a hosting
customer only sees their own websites). A *read-only* token may only use `GET`. Tokens can expire and
be revoked. There are no cookies and no CSRF token.

**Responses.** Success: `{"success": true, "data": …}`. Failure: `{"success": false, "error": {"code", "message", …}}`
with `reason`/`action` (what went wrong and what to do) or `fields` (one message per invalid input).

**Long operations** answer `202 Accepted` with a `job_id`; poll `GET /jobs/{id}` until `done` is true.

**Limits.** 120 requests per minute per token (`429 rate_limited`). Select another managed server with the
`X-NimVor-Server` header.
MD;
    }

    /** @return array<string, mixed> */
    private static function errorSchema(): array
    {
        return [
            'type' => 'object',
            'required' => ['success', 'error'],
            'properties' => [
                'success' => ['const' => false],
                'error' => [
                    'type' => 'object',
                    'required' => ['code', 'message'],
                    'properties' => [
                        'code' => ['type' => 'string', 'description' => 'A stable machine-readable code.', 'enum' => ['unauthorized', 'forbidden', 'read_only_token', 'rate_limited', 'not_found', 'unknown_server', 'validation_failed', 'rejected', 'agent_unavailable', 'agent_error']],
                        'message' => ['type' => 'string', 'description' => 'What happened, in plain words.'],
                        'reason' => ['type' => 'string', 'description' => 'Why (technical detail, also in the panel log).'],
                        'action' => ['type' => 'string', 'description' => 'What to do about it.'],
                        'fields' => ['type' => 'object', 'description' => 'Input name → what is wrong with it.', 'additionalProperties' => ['type' => 'string']],
                    ],
                ],
            ],
        ];
    }

    /** @return array<string, mixed> */
    private static function errorResponses(): array
    {
        $r = static fn (string $d, string $code, string $message, array $extra = []): array => ['description' => $d, 'content' => ['application/json' => [
            'schema' => self::ref('Error'),
            'example' => ['success' => false, 'error' => ['code' => $code, 'message' => $message] + $extra],
        ]]];
        return [
            'Unauthorized' => $r('The token is missing, wrong, expired or revoked.', 'unauthorized', 'A valid API token is required (Authorization: Bearer nimvor_…).'),
            'Forbidden' => $r('The token may not do this: it is read-only, or its user lacks the permission or the hosting plan lacks the feature.', 'forbidden', "The token's user lacks the permission websites.manage."),
            'NotFound' => $r('It does not exist, or it is not visible to this token.', 'not_found', 'Not found, or not visible to this token.'),
            'Conflict' => $r('The change was refused (for example a limit, a name already in use or a safety check); `reason` and `action` say why and what to do.', 'rejected', 'This domain is already used by another website.', ['reason' => 'domain exists', 'action' => 'Choose another domain.']),
            'ValidationFailed' => $r('An input is invalid; `fields` names each one.', 'validation_failed', 'Please correct the highlighted fields.', ['fields' => ['domain' => 'Enter a valid domain name.']]),
            'RateLimited' => $r('More than 120 requests in a minute with this token.', 'rate_limited', 'Too many requests; at most 120 per minute per token.'),
            'AgentError' => $r('The server could not carry the change out (the agent refused it or is unavailable). Nothing was changed.', 'agent_error', 'The web server configuration was not applied.', ['reason' => 'nginx -t failed']),
        ];
    }
}
