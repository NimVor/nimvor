<?php

// Fills an installed panel's database with realistic data, using that
// panel's own code (run with WHACP_CONFIG set, from the panel directory):
//   php populate.php <panel-dir> <state-file>
// Writes the secrets it created (password, API token, encrypted value) to
// <state-file> as JSON, for verification after an upgrade.

declare(strict_types=1);

[$_, $panel, $stateFile] = $argv;
require $panel . '/vendor/autoload.php';

use WhaCP\Core\Application;
use WhaCP\Core\Config;
use WhaCP\Database\Connection;

$defaults = require $panel . '/config/app.php';
$app = new Application(new Config(array_replace_recursive($defaults, require getenv('WHACP_CONFIG'))));
$app->migrate();
$c = $app->container;
$db = $c->get(Connection::class);
$roles = $c->get(\WhaCP\Repositories\RoleRepository::class);
$users = $c->get(\WhaCP\Services\UserService::class);
$now = gmdate('c');

$password = 'fixture-Admin-pass-2468';
$ids = [];
foreach ([['fx-admin', 'admin'], ['fx-reseller', 'reseller'], ['fx-customer', 'customer'], ['fx-viewer', 'viewer']] as [$name, $role]) {
    $ids[$name] = $users->create(['username' => $name, 'email' => $name . '@example.test', 'password' => $password, 'password_confirmation' => $password, 'role_id' => (int) $roles->findByName($role)['id'], 'is_active' => true]);
}
$db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$ids['fx-reseller'], $ids['fx-customer']]);

$token = $c->get(\WhaCP\Services\ApiTokenService::class)->create($ids['fx-admin'], ['name' => 'fixture', 'ability' => 'write', 'expires_days' => 365]);
$c->get(\WhaCP\Repositories\SettingRepository::class)->set('panel_name', 'Fixture Hosting', null);

$crypto = $c->get(\WhaCP\Security\Crypto::class);
$db->run("INSERT INTO servers (name, kind, url, token_enc, created_at) VALUES ('edge-1', 'remote', 'https://192.0.2.10:8443', ?, ?)", [$crypto->encrypt('remote-agent-token-fixture'), $now]);

for ($i = 1; $i <= 25; $i++) {
    $owner = $i % 3 === 0 ? $ids['fx-customer'] : $ids['fx-admin'];
    $domain = "site{$i}.fixture.test";
    $db->run("INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, php_version, created_at, updated_at) VALUES (1, ?, ?, ?, ?, ?, 'php', '8.3', ?, ?)",
        [$owner, $domain, "fx{$i}", "/home/whacp/{$domain}", "/home/whacp/{$domain}/public_html", $now, $now]);
    $site = (int) $db->scalar('SELECT id FROM websites WHERE domain = ?', [$domain]);
    $db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ?, ?, ?, ?)", [$site, $owner, "fx_db{$i}", $now]);
    $db->run("INSERT INTO backups (server_id, website_id, kind, domain, backup_id, label, size, encrypted, status, created_at, finished_at) VALUES (1, ?, 'site', ?, ?, 'fixture', 1024, 1, 'completed', ?, ?)",
        [$site, $domain, sprintf('20260101T000000Z-%06d', $i), $now, $now]);
}
$c->get(\WhaCP\Services\AuditLogger::class)->record('fixture.populated', 'server', '1', \WhaCP\Services\AuditLogger::SUCCESS, ['websites' => 25]);

$counts = [];
foreach ($db->fetchAll("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'") as $t) {
    $counts[$t['name']] = (int) $db->scalar('SELECT COUNT(*) FROM "' . $t['name'] . '"');
}
file_put_contents($stateFile, json_encode(['password' => $password, 'token' => $token, 'counts' => $counts], JSON_PRETTY_PRINT));
echo 'Populated: ' . count($counts) . " tables\n";
