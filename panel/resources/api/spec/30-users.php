<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$user = [
    'id' => 12, 'username' => 'bluebird', 'email' => 'owner@bluebird-cafe.example', 'display_name' => 'Bluebird Cafe',
    'role' => ['id' => 4, 'name' => 'customer', 'label' => 'Hosting customer', 'scope' => 'own'],
    'is_active' => true, 'suspended' => false, 'suspend_reason' => '', 'plan_id' => 2, 'plan_name' => 'Starter', 'parent_id' => null,
    'last_login_at' => '2026-11-23T08:15:00Z', 'created_at' => '2026-11-01T10:00:00Z',
];
$plan = ['id' => 2, 'name' => 'Starter', 'description' => '5 websites, 10 GB', 'owner_id' => null, 'users' => 14, 'limits' => ['max_websites' => 5, 'max_databases' => 10, 'max_email_accounts' => 20, 'disk_quota_mb' => 10240]];
$id = ['$ref' => '#/components/parameters/Id'];
$server = ['$ref' => '#/components/parameters/Server'];
$userRef = O::ref('User');

return [
    'tags' => [
        ['name' => 'Users', 'description' => "Panel users: administrators, staff, resellers and hosting customers.\n\nThe rules of the web interface apply: nobody can hand out a role whose permissions they lack, the last active administrator cannot be removed, disabled or demoted, nobody can delete or disable their own account, and a reseller only sees and manages their own customers. Two-factor resets and role editing are not part of the API."],
        ['name' => 'Accounts', 'description' => "Hosting accounts: a customer (or reseller) login with its plan, hosting options and first website, created in one step, and what administrators do with them afterwards: suspend, resume, change plan, terminate."],
        ['name' => 'Plans', 'description' => 'Hosting plans (resource limits) that can be assigned to accounts.'],
    ],
    'schemas' => [
        'User' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'username' => ['type' => 'string'],
                'email' => ['type' => 'string', 'format' => 'email'],
                'display_name' => ['type' => 'string'],
                'role' => ['type' => 'object', 'description' => 'The user\'s role.', 'properties' => [
                    'id' => ['type' => 'integer'],
                    'name' => ['type' => 'string', 'description' => 'For example `admin`, `operator`, `viewer`, `reseller`, `customer`, or a custom role.'],
                    'label' => ['type' => 'string'],
                    'scope' => ['type' => 'string', 'enum' => ['all', 'own'], 'description' => '`own`: the user only sees their own resources (a hosting account).'],
                ]],
                'is_active' => ['type' => 'boolean', 'description' => 'False: the user cannot sign in.'],
                'suspended' => ['type' => 'boolean', 'description' => 'The account (or its reseller) is suspended: its services are off.'],
                'suspend_reason' => ['type' => 'string'],
                'plan_id' => ['type' => ['integer', 'null']],
                'plan_name' => ['type' => ['string', 'null']],
                'parent_id' => ['type' => ['integer', 'null'], 'description' => 'The reseller this customer belongs to.'],
                'last_login_at' => ['type' => ['string', 'null'], 'format' => 'date-time'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
        'Plan' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'name' => ['type' => 'string'],
                'description' => ['type' => 'string'],
                'owner_id' => ['type' => ['integer', 'null'], 'description' => 'The reseller who owns the plan; null for the administrators\' plans.'],
                'users' => ['type' => 'integer', 'description' => 'How many accounts use it.'],
                'limits' => ['type' => 'object', 'description' => 'Resource limits by name (`max_websites`, `max_databases`, `max_email_accounts`, `disk_quota_mb`, …); 0 means unlimited.', 'additionalProperties' => ['type' => 'integer']],
            ],
        ],
    ],
    'paths' => [
        '/users' => [
            'get' => [
                'operationId' => 'listUsers',
                'tags' => ['Users'],
                'summary' => 'List users',
                'description' => 'Everyone for administrators and staff; a reseller sees only their own customers.',
                'x-permission' => 'users.view',
                'responses' => ['200' => O::res('The users, by name.', ['type' => 'array', 'items' => $userRef], [$user])],
            ],
            'post' => [
                'operationId' => 'createUser',
                'tags' => ['Users'],
                'summary' => 'Create a user',
                'description' => 'For a full hosting account with a website, use `POST /accounts`. A reseller can only create hosting customers, within their plan\'s customer limit. The generated password is returned once.',
                'x-permission' => 'users.manage',
                'requestBody' => O::body([
                    'type' => 'object',
                    'required' => ['username', 'email', 'role_id'],
                    'properties' => [
                        'username' => ['type' => 'string', 'pattern' => '^[a-z][a-z0-9._-]{2,31}$', 'description' => '3–32 lowercase letters, digits, dots, dashes or underscores, starting with a letter.'],
                        'email' => ['type' => 'string', 'format' => 'email'],
                        'display_name' => ['type' => 'string', 'maxLength' => 100],
                        'role_id' => ['type' => 'integer', 'description' => 'A role whose permissions you hold yourself.'],
                        'password' => ['type' => 'string', 'description' => 'Must pass the password policy. Omit when `generate_password` is true.'],
                        'generate_password' => ['type' => 'boolean', 'default' => false, 'description' => 'Create a strong password and return it once in the response.'],
                        'plan_id' => ['type' => 'integer', 'description' => 'A plan you may assign (see `GET /plans`).'],
                        'is_active' => ['type' => 'boolean', 'default' => true],
                    ],
                ], ['username' => 'jsmith', 'email' => 'j.smith@example.com', 'display_name' => 'J. Smith', 'role_id' => 3, 'generate_password' => true]),
                'responses' => [
                    '201' => O::res('Created. `password` is set only when it was generated.', ['type' => 'object', 'properties' => ['user' => $userRef, 'password' => ['type' => ['string', 'null'], 'description' => 'Shown once.']]], ['user' => $user, 'password' => 'Xq7-mPz2…']),
                    '409' => O::err('Conflict'),
                    '422' => O::err('ValidationFailed'),
                ],
            ],
        ],
        '/users/{id}' => [
            'get' => [
                'operationId' => 'getUser',
                'tags' => ['Users'],
                'summary' => 'Get a user',
                'x-permission' => 'users.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The user.', $userRef, $user), '404' => O::err('NotFound')],
            ],
            'patch' => [
                'operationId' => 'updateUser',
                'tags' => ['Users'],
                'summary' => 'Change a user',
                'description' => 'Only the fields you send change. Changing a password signs the user out of other sessions. Changing the role needs a role whose permissions you hold, and the last active administrator cannot be demoted or disabled.',
                'x-permission' => 'users.manage',
                'parameters' => [$id],
                'requestBody' => O::body([
                    'type' => 'object',
                    'properties' => [
                        'email' => ['type' => 'string', 'format' => 'email'],
                        'display_name' => ['type' => 'string', 'maxLength' => 100],
                        'role_id' => ['type' => 'integer'],
                        'is_active' => ['type' => 'boolean', 'description' => 'False disables sign-in (not for yourself).'],
                        'password' => ['type' => 'string', 'description' => 'A new password; must pass the password policy.'],
                        'plan_id' => ['type' => ['integer', 'null'], 'description' => 'A plan you may assign.'],
                    ],
                ], ['display_name' => 'J. Smith (billing)', 'is_active' => true]),
                'responses' => ['200' => O::res('The user, changed.', $userRef, ['display_name' => 'J. Smith (billing)'] + $user), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed')],
            ],
            'delete' => [
                'operationId' => 'deleteUser',
                'tags' => ['Users'],
                'summary' => 'Delete a user',
                'description' => 'Removes the panel login only. For a hosting account with websites, mail and databases use `POST /users/{id}/terminate`. You cannot delete yourself or the last active administrator.',
                'x-permission' => 'users.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer']]], ['deleted' => 12]), '404' => O::err('NotFound'), '409' => O::err('Conflict')],
            ],
        ],
        '/accounts' => ['post' => [
            'operationId' => 'createAccount',
            'tags' => ['Accounts'],
            'summary' => 'Create a hosting account',
            'description' => 'In one step: the customer (or reseller) login, its plan and hosting options, and, when `domain` is given, its first website run by a new system account of the same name, with its DNS zone, mail and automatic SSL. If the website cannot be created the login is removed again.',
            'x-permission' => 'users.manage',
            'requestBody' => O::body([
                'type' => 'object',
                'required' => ['username', 'email'],
                'properties' => [
                    'username' => ['type' => 'string', 'pattern' => '^[a-z][a-z0-9_-]{2,30}$', 'description' => 'Also the name of the website\'s system account.'],
                    'email' => ['type' => 'string', 'format' => 'email'],
                    'display_name' => ['type' => 'string'],
                    'password' => ['type' => 'string', 'description' => 'Must pass the password policy. Omit when `generate_password` is true.'],
                    'generate_password' => ['type' => 'boolean', 'default' => false, 'description' => 'Create a strong password and return it once.'],
                    'domain' => ['type' => 'string', 'description' => 'The first website\'s domain. Omit to create the login only.', 'examples' => ['bluebird-cafe.example']],
                    'php_version' => ['type' => 'string', 'description' => 'PHP version of the website (default: the server\'s default).'],
                    'www' => ['type' => 'boolean', 'default' => true, 'description' => 'Also serve `www.<domain>`.'],
                    'plan_id' => ['type' => 'integer', 'description' => 'A plan you may assign.'],
                    'reseller' => ['type' => 'boolean', 'default' => false, 'description' => 'Create a reseller instead of a customer (administrators only).'],
                    'dns_dkim' => ['type' => 'boolean', 'description' => 'Hosting option: publish DKIM in the website\'s DNS zone.'],
                    'dns_spf' => ['type' => 'boolean', 'description' => 'Hosting option: publish SPF.'],
                    'dns_dmarc' => ['type' => 'boolean', 'description' => 'Hosting option: publish DMARC (needs DKIM and SPF).'],
                    'spam_filter' => ['type' => 'boolean', 'description' => 'Hosting option: filter the account\'s mail for spam.'],
                    'spam_box' => ['type' => 'boolean', 'description' => 'Hosting option: deliver spam to a Spam folder.'],
                    'mail_routing' => ['type' => 'string', 'description' => 'Hosting option: `auto`, `local` or `remote`.', 'default' => 'auto'],
                ],
            ], ['username' => 'bluebird', 'email' => 'owner@bluebird-cafe.example', 'display_name' => 'Bluebird Cafe', 'generate_password' => true, 'domain' => 'bluebird-cafe.example', 'plan_id' => 2]),
            'responses' => [
                '201' => O::res('The account, its first website (when a domain was given) and the generated password (once).', ['type' => 'object', 'properties' => [
                    'user' => $userRef,
                    'website_id' => ['type' => ['integer', 'null'], 'description' => 'The website\'s id for `GET /websites/{id}`.'],
                    'password' => ['type' => ['string', 'null'], 'description' => 'Shown once.'],
                ]], ['user' => $user, 'website_id' => 21, 'password' => 'Xq7-mPz2…']),
                '409' => O::err('Conflict'),
                '422' => O::err('ValidationFailed'),
                '502' => O::err('AgentError'),
            ],
        ]],
        '/users/{id}/suspend' => ['post' => [
            'operationId' => 'suspendAccount',
            'tags' => ['Accounts'],
            'summary' => 'Suspend a hosting account',
            'description' => 'Administrators only. The account cannot sign in; its websites, email logins, databases, FTP/SFTP, cron jobs and workers are turned off. **Nothing is deleted.** Suspending a reseller suspends their customers too. Repeat the call to retry parts that failed (`warnings`).',
            'x-permission' => 'users.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'properties' => ['reason' => ['type' => 'string', 'maxLength' => 250, 'description' => 'A note kept with the account.']]], ['reason' => 'Unpaid invoice'], false),
            'responses' => ['200' => O::res('The account, suspended. `warnings` lists services that could not be turned off yet.', ['type' => 'object', 'properties' => ['user' => $userRef, 'warnings' => ['type' => 'array', 'items' => ['type' => 'string']]]], ['user' => ['suspended' => true, 'suspend_reason' => 'Unpaid invoice'] + $user, 'warnings' => []]), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '403' => O::err('Forbidden')],
        ]],
        '/users/{id}/unsuspend' => ['post' => [
            'operationId' => 'unsuspendAccount',
            'tags' => ['Accounts'],
            'summary' => 'Resume a suspended account',
            'description' => 'Administrators only. Turns the account\'s services back on.',
            'x-permission' => 'users.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The account, resumed.', ['type' => 'object', 'properties' => ['user' => $userRef, 'warnings' => ['type' => 'array', 'items' => ['type' => 'string']]]], ['user' => $user, 'warnings' => []]), '404' => O::err('NotFound'), '409' => O::err('Conflict')],
        ]],
        '/users/{id}/plan' => ['put' => [
            'operationId' => 'changeAccountPlan',
            'tags' => ['Accounts'],
            'summary' => 'Change an account\'s plan',
            'description' => 'The new limits apply from now on; nothing is removed. CPU, memory and other resource limits are applied at once (`warnings` says if that has to wait for the scheduler).',
            'x-permission' => 'users.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['plan_id'], 'properties' => ['plan_id' => ['type' => 'integer', 'description' => 'A plan you may assign (see `GET /plans`).']]], ['plan_id' => 3]),
            'responses' => ['200' => O::res('The account and its new plan.', ['type' => 'object', 'properties' => ['user' => $userRef, 'plan' => O::ref('Plan'), 'warnings' => ['type' => 'array', 'items' => ['type' => 'string']]]], ['user' => ['plan_id' => 3, 'plan_name' => 'Business'] + $user, 'plan' => ['id' => 3, 'name' => 'Business'] + $plan, 'warnings' => []]), '404' => O::err('NotFound'), '409' => O::err('Conflict')],
        ]],
        '/users/{id}/terminate' => ['post' => [
            'operationId' => 'terminateAccount',
            'tags' => ['Accounts'],
            'summary' => 'Terminate a hosting account',
            'description' => '**Destructive and not reversible.** Administrators only. Deletes the account\'s websites with their files and system accounts, mail domains and mailboxes, databases and DNS zones, then the login. A reseller can only be terminated after all of their customers. To guard against mistakes the request must contain `"confirm": "TERMINATE"`. Take a backup first (`POST /websites/{id}/backups`).',
            'x-permission' => 'users.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['confirm'], 'properties' => ['confirm' => ['type' => 'string', 'const' => 'TERMINATE', 'description' => 'The word TERMINATE, exactly.']]], ['confirm' => 'TERMINATE']),
            'responses' => ['200' => O::res('Terminated.', ['type' => 'object', 'properties' => ['terminated' => ['type' => 'integer'], 'message' => ['type' => 'string', 'description' => 'What was removed.']]], ['terminated' => 12, 'message' => 'Terminated: 1 website(s), 3 mailbox(es), 1 database(s), 1 DNS zone(s).']), '403' => O::err('Forbidden'), '404' => O::err('NotFound'), '409' => O::err('Conflict')],
        ]],
        '/plans' => ['get' => [
            'operationId' => 'listPlans',
            'tags' => ['Plans'],
            'summary' => 'List hosting plans',
            'description' => 'The plans the token may assign: all of them for administrators, a reseller\'s own plans for a reseller.',
            'x-permission' => 'users.view',
            'responses' => ['200' => O::res('The plans.', ['type' => 'array', 'items' => O::ref('Plan')], [$plan])],
        ]],
    ],
];
