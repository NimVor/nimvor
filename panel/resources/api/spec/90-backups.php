<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$part = ['name' => 'files.tar.zst.enc', 'kind' => 'files', 'database' => null, 'size' => 52428800];
$backup = ['id' => 12, 'backup_id' => '20261123T200100Z-3f9a1c', 'label' => 'before upgrade', 'status' => 'completed', 'size' => 73400320, 'encrypted' => true, 'scheduled' => false, 'on_server' => true, 'storage_id' => null, 'parts' => [$part, ['name' => 'db-shop.sql.zst.enc', 'kind' => 'database', 'database' => 'shop', 'size' => 20971520]], 'created_at' => '2026-11-23T20:01:00Z', 'finished_at' => '2026-11-23T20:03:10Z', 'error' => null];
$schedule = ['id' => 3, 'frequency' => 'daily', 'hour' => 3, 'weekday' => 0, 'retention' => 7, 'include_files' => true, 'include_databases' => true, 'encrypt' => true, 'storage_id' => null, 'keep_local' => true, 'enabled' => true, 'last_run_at' => '2026-11-24T03:00:05Z'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$bk = ['name' => 'backup', 'in' => 'path', 'required' => true, 'description' => 'The backup\'s `id` (not its `backup_id`).', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$sc = ['name' => 'schedule', 'in' => 'path', 'required' => true, 'description' => 'The schedule\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$bref = O::ref('BackupDetail');
$sref = O::ref('BackupSchedule');

return [
    'tags' => [
        ['name' => 'Backup schedules', 'description' => "Automatic backups of a website. Schedules are set up by administrators only; an account whose plan does not include automatic backups neither sees the schedules nor the copies they make (`scheduled: true`), and gets `404` for them.\n\nA schedule runs daily or weekly at a full hour (server time) and keeps the newest `retention` copies, deleting older completed ones."],
    ],
    'schemas' => [
        'BackupDetail' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'backup_id' => ['type' => 'string', 'description' => 'The backup\'s identifier on the server; empty while it is still running.'],
                'label' => ['type' => 'string'],
                'status' => ['type' => 'string', 'enum' => ['running', 'completed', 'failed']],
                'size' => ['type' => 'integer', 'description' => 'Bytes.'],
                'encrypted' => ['type' => 'boolean'],
                'scheduled' => ['type' => 'boolean', 'description' => 'Made by an automatic schedule rather than on request.'],
                'on_server' => ['type' => 'boolean', 'description' => 'A copy is stored on this server. False when it is kept in remote storage only.'],
                'storage_id' => ['type' => ['integer', 'null'], 'description' => 'The remote storage that holds a copy, if any.'],
                'parts' => ['type' => 'array', 'description' => 'What the backup contains. Database names can be passed to a restore.', 'items' => ['type' => 'object', 'properties' => [
                    'name' => ['type' => 'string'], 'kind' => ['type' => 'string', 'enum' => ['files', 'database']],
                    'database' => ['type' => ['string', 'null']], 'size' => ['type' => 'integer'],
                ]]],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
                'finished_at' => ['type' => ['string', 'null'], 'format' => 'date-time'],
                'error' => ['type' => ['string', 'null']],
            ],
        ],
        'BackupSchedule' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'frequency' => ['type' => 'string', 'enum' => ['daily', 'weekly']],
                'hour' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 23],
                'weekday' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 6, 'description' => '0 is Sunday. Only used when weekly.'],
                'retention' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 365, 'description' => 'How many backups of this schedule to keep.'],
                'include_files' => ['type' => 'boolean'],
                'include_databases' => ['type' => 'boolean'],
                'encrypt' => ['type' => 'boolean'],
                'storage_id' => ['type' => ['integer', 'null']],
                'keep_local' => ['type' => 'boolean', 'description' => 'Keep a copy on the server when a remote storage is used.'],
                'enabled' => ['type' => 'boolean'],
                'last_run_at' => ['type' => ['string', 'null'], 'format' => 'date-time'],
            ],
        ],
    ],
    'paths' => [
        '/websites/{id}/backups/{backup}' => [
            'get' => [
                'operationId' => 'getWebsiteBackup',
                'tags' => ['Backups'],
                'summary' => 'Get a backup',
                'description' => 'One backup with the parts it contains. To start a backup use `POST /websites/{id}/backups`; follow it with `GET /jobs/{id}` or poll this endpoint until `status` is `completed`.',
                'x-permission' => 'backups.view',
                'parameters' => [$id, $bk],
                'responses' => ['200' => O::res('The backup.', $bref, $backup), '404' => O::err('NotFound')],
            ],
            'delete' => [
                'operationId' => 'deleteWebsiteBackup',
                'tags' => ['Backups'],
                'summary' => 'Delete a backup',
                'description' => 'Deletes the stored copy (and the one in remote storage, if it is still configured). A backup that is still running cannot be deleted (`409`).',
                'x-permission' => 'backups.manage',
                'parameters' => [$id, $bk],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer']]], ['deleted' => 12]), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/backups/{backup}/restore' => ['post' => [
            'operationId' => 'restoreWebsiteBackup',
            'tags' => ['Backups'],
            'summary' => 'Restore a backup',
            'description' => 'Restores the website\'s files, some of its databases, or both. **This overwrites the current files and the named databases.** A safety backup of the current files is taken first. Send `confirm` with the website\'s domain name. Returns `202` with a `job_id`: follow it with `GET /jobs/{id}`.',
            'x-permission' => 'backups.manage',
            'parameters' => [$id, $bk],
            'requestBody' => O::body(['type' => 'object', 'required' => ['confirm'], 'properties' => [
                'confirm' => ['type' => 'string', 'description' => 'The website\'s domain name, exactly.', 'examples' => ['bluebird-cafe.example']],
                'files' => ['type' => 'boolean', 'default' => false, 'description' => 'Restore the website\'s files.'],
                'databases' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Names of databases in the backup to restore, each into the database of the same name.'],
            ]], ['confirm' => 'bluebird-cafe.example', 'files' => true, 'databases' => ['shop']]),
            'responses' => ['202' => O::res('The restore started.', O::ref('Job'), ['job_id' => '3f9a1c2b7d4e5a60']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/backup-schedules' => [
            'get' => [
                'operationId' => 'listBackupSchedules',
                'tags' => ['Backup schedules'],
                'summary' => 'List a website\'s backup schedules',
                'x-permission' => 'backups.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The schedules (an empty list for an account that does not see automatic backups).', ['type' => 'array', 'items' => $sref], [$schedule]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'createBackupSchedule',
                'tags' => ['Backup schedules'],
                'summary' => 'Add a backup schedule',
                'description' => 'Administrators only (`403` otherwise). A new schedule waits for its first slot instead of running at once.',
                'x-permission' => 'backups.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'properties' => [
                    'frequency' => ['type' => 'string', 'enum' => ['daily', 'weekly'], 'default' => 'daily'],
                    'hour' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 23, 'default' => 3],
                    'weekday' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 6, 'default' => 0],
                    'retention' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 365, 'default' => 7],
                    'include_files' => ['type' => 'boolean', 'default' => true],
                    'include_databases' => ['type' => 'boolean', 'default' => true],
                    'encrypt' => ['type' => 'boolean', 'default' => true],
                    'storage_id' => ['type' => ['integer', 'null'], 'description' => 'An active remote storage to copy to.'],
                    'keep_local' => ['type' => 'boolean', 'default' => true],
                ]], ['frequency' => 'daily', 'hour' => 3, 'retention' => 7]),
                'responses' => ['201' => O::res('The schedule.', $sref, $schedule), '403' => O::err('Forbidden'), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed')],
            ],
        ],
        '/websites/{id}/backup-schedules/{schedule}' => ['delete' => [
            'operationId' => 'deleteBackupSchedule',
            'tags' => ['Backup schedules'],
            'summary' => 'Delete a backup schedule',
            'description' => 'Administrators only. The backups it already made are kept.',
            'x-permission' => 'backups.manage',
            'parameters' => [$id, $sc],
            'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer']]], ['deleted' => 3]), '403' => O::err('Forbidden'), '404' => O::err('NotFound')],
        ]],
    ],
];
