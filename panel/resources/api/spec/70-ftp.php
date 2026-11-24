<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$account = ['login' => 'designer@bluebird-cafe.example', 'dir' => 'public_html', 'quota_mb' => 500];
$sftp = ['enabled' => true, 'password_login' => false, 'keys' => ['ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample designer@laptop'], 'root' => '/home/nimvor/sites'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$lg = ['name' => 'name', 'in' => 'path', 'required' => true, 'description' => 'The account\'s name: the part of its login before the `@`.', 'schema' => ['type' => 'string'], 'example' => 'designer'];
$acRef = O::ref('FtpAccount');
$props = [
    'dir' => ['type' => 'string', 'maxLength' => 300, 'description' => 'The folder inside the website the account is confined to, for example `public_html`. Empty means the whole website.', 'examples' => ['public_html']],
    'quota_mb' => ['type' => 'integer', 'minimum' => 0, 'description' => 'Disk quota in MB; 0 means none.'],
    'password' => ['type' => 'string', 'minLength' => 10, 'maxLength' => 128, 'description' => 'The new password.'],
    'generate' => ['type' => 'boolean', 'description' => 'Generate a strong password instead; it is returned once in the response.'],
];
$created = ['type' => 'object', 'allOf' => [$acRef], 'properties' => ['password' => ['type' => 'string', 'description' => 'Only present when `generate` was true. It is shown once and cannot be read again.']]];

return [
    'tags' => [
        ['name' => 'FTP and SFTP', 'description' => "FTP accounts and SFTP access of a website.\n\nAn FTP account is a virtual login named `<name>@<website domain>`, confined to one folder of its website; the FTP server uses TLS by default. A website can have at most 25 accounts and the plan's FTP limit applies. SFTP uses the website's own system account, chrooted to its folder; it is off until you enable it. The FTP server itself (install, ports, TLS) is configured by an administrator in the panel."],
    ],
    'schemas' => [
        'FtpAccount' => [
            'type' => 'object',
            'properties' => [
                'login' => ['type' => 'string', 'description' => 'The full login name.'],
                'dir' => ['type' => 'string', 'description' => 'The folder the account is confined to.'],
                'quota_mb' => ['type' => 'integer', 'description' => 'Disk quota in MB; 0 means none.'],
            ],
        ],
        'SftpAccess' => [
            'type' => 'object',
            'properties' => [
                'enabled' => ['type' => 'boolean'],
                'password_login' => ['type' => 'boolean', 'description' => 'Whether a password is accepted besides keys.'],
                'keys' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Authorized public keys, one per entry.'],
                'root' => ['type' => 'string', 'description' => 'The folder that holds the websites; a website\'s files are under `<root>/<domain>`.'],
            ],
        ],
    ],
    'paths' => [
        '/websites/{id}/ftp' => [
            'get' => [
                'operationId' => 'listFtpAccounts',
                'tags' => ['FTP and SFTP'],
                'summary' => 'List a website\'s FTP accounts',
                'x-permission' => 'ftp.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The accounts, and whether the FTP server is running.', ['type' => 'object', 'properties' => [
                    'server_ready' => ['type' => 'boolean', 'description' => 'False while the administrator has not set up or has stopped the FTP server; accounts can still be listed.'],
                    'tls' => ['type' => 'string', 'description' => '`required` or `optional`.'],
                    'accounts' => ['type' => 'array', 'items' => $acRef],
                ]], ['server_ready' => true, 'tls' => 'required', 'accounts' => [$account]]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
            'post' => [
                'operationId' => 'createFtpAccount',
                'tags' => ['FTP and SFTP'],
                'summary' => 'Create an FTP account',
                'description' => 'Send `name` (lower-case letters, digits and `. _ -`, up to 31) and either a `password` or `generate: true`. The login becomes `<name>@<website domain>`. A generated password is returned once.',
                'x-permission' => 'ftp.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['name'], 'properties' => ['name' => ['type' => 'string', 'description' => 'The part before the `@`.', 'examples' => ['designer']]] + $props], ['name' => 'designer', 'dir' => 'public_html', 'quota_mb' => 500, 'generate' => true]),
                'responses' => ['201' => O::res('The account.', $created, $account + ['password' => 'Kq9ZsAa3mR1tVxB7pLw2']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/ftp/{name}' => [
            'patch' => [
                'operationId' => 'updateFtpAccount',
                'tags' => ['FTP and SFTP'],
                'summary' => 'Change an FTP account',
                'description' => 'Only the fields you send change. Send `password` or `generate: true` to set a new password; without either the password stays as it is.',
                'x-permission' => 'ftp.manage',
                'parameters' => [$id, $lg],
                'requestBody' => O::body(['type' => 'object', 'properties' => $props], ['quota_mb' => 1000]),
                'responses' => ['200' => O::res('The account, changed.', $created, ['quota_mb' => 1000] + $account), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteFtpAccount',
                'tags' => ['FTP and SFTP'],
                'summary' => 'Delete an FTP account',
                'description' => 'The login stops working at once. The files it could reach are kept.',
                'x-permission' => 'ftp.manage',
                'parameters' => [$id, $lg],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'string']]], ['deleted' => 'designer@bluebird-cafe.example']), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/sftp' => [
            'get' => [
                'operationId' => 'getSftpAccess',
                'tags' => ['FTP and SFTP'],
                'summary' => 'Get a website\'s SFTP access',
                'x-permission' => 'ftp.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The SFTP settings.', O::ref('SftpAccess'), $sftp), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
            'put' => [
                'operationId' => 'updateSftpAccess',
                'tags' => ['FTP and SFTP'],
                'summary' => 'Change a website\'s SFTP access',
                'description' => 'Only the fields you send change. `keys` replaces the whole list of authorized keys. A `password` (10–128 characters) sets the account\'s SFTP password and is only used when `password_login` is true.',
                'x-permission' => 'ftp.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'properties' => [
                    'enabled' => ['type' => 'boolean'],
                    'password_login' => ['type' => 'boolean'],
                    'keys' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Public keys in OpenSSH format.'],
                    'password' => ['type' => 'string', 'minLength' => 10, 'maxLength' => 128],
                ]], ['enabled' => true, 'keys' => $sftp['keys']]),
                'responses' => ['200' => O::res('The SFTP settings, changed.', O::ref('SftpAccess'), $sftp), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
    ],
];
