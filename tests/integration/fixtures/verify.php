<?php

// Verifies an upgraded panel still has everything populate.php created:
//   php verify.php <panel-dir> <state-file>

declare(strict_types=1);

[$_, $panel, $stateFile] = $argv;
require $panel . '/vendor/autoload.php';

use NimVor\Core\Application;
use NimVor\Core\Config;
use NimVor\Database\Connection;

$state = json_decode((string) file_get_contents($stateFile), true);
$defaults = require $panel . '/config/app.php';
$app = new Application(new Config(array_replace_recursive($defaults, require getenv('NIMVOR_CONFIG'))));
$c = $app->container;
$db = $c->get(Connection::class);
$errors = [];

foreach ($state['counts'] as $table => $before) {
    try {
        $after = (int) $db->scalar('SELECT COUNT(*) FROM "' . $table . '"');
    } catch (\Throwable $e) {
        $errors[] = "table {$table} is gone";
        continue;
    }
    // Upgrades may add rows (audit entries, new defaults) but never lose any.
    if ($after < $before) {
        $errors[] = "table {$table}: {$before} rows before the upgrade, {$after} after";
    }
}
$hash = (string) $db->scalar("SELECT password_hash FROM users WHERE username = 'fx-admin'");
if (!password_verify($state['password'], $hash)) {
    $errors[] = 'the administrator password no longer verifies';
}
$enc = (string) $db->scalar("SELECT token_enc FROM servers WHERE name = 'edge-1'");
try {
    if ($c->get(\NimVor\Security\Crypto::class)->decrypt($enc) !== 'remote-agent-token-fixture') {
        $errors[] = 'the remote server token decrypts to something else';
    }
} catch (\Throwable $e) {
    $errors[] = 'the remote server token no longer decrypts: ' . $e->getMessage();
}
if ((string) $db->scalar("SELECT parent_id FROM users WHERE username = 'fx-customer'") !== (string) $db->scalar("SELECT id FROM users WHERE username = 'fx-reseller'")) {
    $errors[] = 'the customer lost its reseller';
}
if ((int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'fixture.populated'") !== 1) {
    $errors[] = 'the audit history was lost';
}
if ($errors !== []) {
    fwrite(STDERR, implode("\n", $errors) . "\n");
    exit(1);
}
echo "All fixture data is intact.\n";
