<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$install = ['id' => 7, 'app' => 'wordpress', 'path' => 'public_html', 'status' => 'succeeded', 'version' => '6.8', 'error' => '', 'admin_user' => 'owner', 'database' => 'bluebird_wp', 'job_id' => '3f9a1c2b7d4e5a60', 'created_at' => '2026-11-24T08:00:00Z', 'finished_at' => '2026-11-24T08:01:30Z'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$app = ['name' => 'app', 'in' => 'path', 'required' => true, 'description' => 'The application\'s `slug` from `GET /apps`.', 'schema' => ['type' => 'string'], 'example' => 'wordpress'];

return [
    'tags' => [
        ['name' => 'Applications', 'description' => "The one-click application installer: WordPress, Laravel and the other applications the server offers.\n\nOnly applications the token's user may install are listed (a hosting customer sees what the provider made visible). An install creates the application's database and user in the same step, runs in the background and answers `202` with a `job_id` to follow with `GET /jobs/{id}`. Only one install into a folder can run at a time. Check what an install would do first with the `plan` endpoint, which changes nothing."],
    ],
    'schemas' => [
        'AppInstall' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'], 'app' => ['type' => 'string'], 'path' => ['type' => 'string', 'description' => 'The folder inside the website.'],
                'status' => ['type' => 'string', 'enum' => ['running', 'succeeded', 'failed', 'unknown']],
                'version' => ['type' => 'string'], 'error' => ['type' => 'string'], 'admin_user' => ['type' => 'string'],
                'database' => ['type' => ['string', 'null'], 'description' => 'The database created for it.'], 'job_id' => ['type' => 'string'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'], 'finished_at' => ['type' => ['string', 'null'], 'format' => 'date-time'],
            ],
        ],
    ],
    'paths' => [
        '/apps' => ['get' => [
            'operationId' => 'listApps',
            'tags' => ['Applications'],
            'summary' => 'List the applications that can be installed',
            'description' => 'Grouped by category. `installer_enabled` is false when the provider turned the installer off (then the list is empty).',
            'x-permission' => 'apps.install',
            'responses' => ['200' => O::res('The catalog.', ['type' => 'object', 'properties' => [
                'installer_enabled' => ['type' => 'boolean'],
                'categories' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => [
                    'category' => ['type' => 'string'], 'name' => ['type' => 'string'],
                    'apps' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => ['slug' => ['type' => 'string'], 'name' => ['type' => 'string'], 'description' => ['type' => 'string'], 'needs_database' => ['type' => 'boolean']]]],
                ]]],
            ]], ['installer_enabled' => true, 'categories' => [['category' => 'blogs-cms', 'name' => 'Blogs & CMS', 'apps' => [['slug' => 'wordpress', 'name' => 'WordPress', 'description' => 'The world\'s most popular blog and website builder.', 'needs_database' => true]]]]]), '403' => O::err('Forbidden')],
        ]],
        '/websites/{id}/apps' => ['get' => [
            'operationId' => 'listWebsiteApps',
            'tags' => ['Applications'],
            'summary' => 'List a website\'s installations',
            'description' => 'Every install into the website, newest first, with its result. Running installs are brought up to date first.',
            'x-permission' => 'apps.install',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The installations.', ['type' => 'array', 'items' => O::ref('AppInstall')], [$install]), '404' => O::err('NotFound')],
        ]],
        '/websites/{id}/apps/{app}/plan' => ['get' => [
            'operationId' => 'planAppInstall',
            'tags' => ['Applications'],
            'summary' => 'Check what an install would do',
            'description' => 'A dry run: the requirements, the changes and any conflicts, worked out by the server without changing anything. `409` when the application does not fit the website (for example a PHP application on a static website).',
            'x-permission' => 'apps.install',
            'parameters' => [$id, $app],
            'responses' => ['200' => O::res('The plan.', ['type' => 'object'], ['ok' => true, 'requirements' => [], 'changes' => ['Download WordPress into public_html', 'Create a database']]), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/apps/{app}' => ['post' => [
            'operationId' => 'installApp',
            'tags' => ['Applications'],
            'summary' => 'Install an application',
            'description' => 'Creates a database and user for it, then installs it in the background. For WordPress (and applications built on it) a strong administrator password is generated and returned **once**. Which fields apply depends on the application: `title`, `admin_user` and `admin_email` for WordPress and the applications that create an administrator, `path` for applications that go in their own folder, `migrate` for Laravel. A folder that already has an install running answers `409`.',
            'x-permission' => 'apps.install',
            'parameters' => [$id, $app],
            'requestBody' => O::body(['type' => 'object', 'properties' => [
                'title' => ['type' => 'string', 'maxLength' => 100, 'description' => 'The site\'s title (WordPress).'],
                'admin_user' => ['type' => 'string', 'pattern' => '^[A-Za-z0-9._@-]{3,60}$', 'default' => 'admin'],
                'admin_email' => ['type' => 'string', 'format' => 'email', 'description' => 'The administrator\'s address.'],
                'path' => ['type' => 'string', 'description' => 'A new folder name for applications that do not install into the document root.', 'examples' => ['app']],
                'migrate' => ['type' => 'boolean', 'description' => 'Laravel: run the database migrations.'],
                'target_id' => ['type' => 'integer', 'description' => 'Install into one of this website\'s subdomains instead (its website id).'],
            ]], ['title' => 'Bluebird Café', 'admin_user' => 'owner', 'admin_email' => 'owner@bluebird-cafe.example'], false),
            'responses' => ['202' => O::res('The install started.', ['type' => 'object', 'properties' => [
                'job_id' => ['type' => 'string'], 'website_id' => ['type' => 'integer', 'description' => 'The website it is installed into.'],
                'admin_user' => ['type' => 'string'], 'admin_password' => ['type' => 'string', 'description' => 'Only for applications that create an administrator. Shown once.'],
            ]], ['job_id' => '3f9a1c2b7d4e5a60', 'website_id' => 21, 'admin_user' => 'owner', 'admin_password' => 'Kq9ZsAa3mR1tVxB7pLw2']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
    ],
];
