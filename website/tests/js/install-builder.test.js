'use strict';
// Run with: node --test tests/js
const test = require('node:test');
const assert = require('node:assert/strict');
const { buildCommand } = require('../../public/assets/js/install-builder.js');

const base = { mode: 'hosting', profile: 'full', hostname: '', adminUser: '', adminEmail: '', php: ['8.3', '8.4'], port: '', publicHttps: false, httpsPort: '', yes: true, resume: false, noThirdParty: false, noDeps: false, forceUnsupported: false, dryRun: false, version: '', commit: '', remote: '', allow: '' };
const opts = (r) => r.command.split(' \\\n  ').slice(1);

test('the default hosting command', () => {
    const r = buildCommand(base);
    assert.deepEqual(r.errors, []);
    assert.equal(r.command.split('\n')[0], 'curl -fsSL https://nimvor.com/install | sudo bash -s -- \\');
    assert.deepEqual(opts(r), ['--yes', '--enable-writes', '--profile=full', '--php=8.3,8.4']);
});

test('every choice turns into the option the installer has', () => {
    const r = buildCommand({ ...base, profile: 'full', hostname: 'server.example.com', adminUser: 'boss', adminEmail: 'boss@mydomain.org', php: ['8.4', '8.2', '8.3'], port: '9000', publicHttps: true, httpsPort: '9443', resume: true, noThirdParty: true, noDeps: true, forceUnsupported: true, dryRun: true, version: '1.1.0', commit: 'abc1234' });
    assert.deepEqual(r.errors, []);
    assert.deepEqual(opts(r), ['--version 1.1.0', '--commit abc1234', '--yes', '--enable-writes', '--profile=full', '--hostname=server.example.com', '--php=8.2,8.3,8.4', '--admin-user=boss', '--admin-email=boss@mydomain.org', '--port=9000', '--public-https=9443', '--resume', '--no-third-party-repos', '--no-install-deps', '--force-unsupported', '--dry-run']);
});

test('the default HTTPS port is written as the bare flag, and the default panel port is left out', () => {
    const r = buildCommand({ ...base, publicHttps: true, httpsPort: '', port: '8090' });
    assert.ok(opts(r).includes('--public-https'));
    assert.ok(!opts(r).some((o) => o.startsWith('--port')));
});

test('profile none and the look-first mode ask for no PHP and no write mode', () => {
    assert.deepEqual(opts(buildCommand({ ...base, profile: 'none' })), ['--yes', '--enable-writes', '--profile=none']);
    const look = buildCommand({ ...base, mode: 'look', yes: false });
    assert.deepEqual(opts(look), []);
    assert.equal(look.command, 'curl -fsSL https://nimvor.com/install | sudo bash -s --');
    assert.ok(look.notes.length >= 1);
});

test('agent only needs an address and the panel server', () => {
    assert.equal(buildCommand({ ...base, mode: 'agent' }).errors.length, 2);
    const r = buildCommand({ ...base, mode: 'agent', remote: '0.0.0.0:8443', allow: '203.0.113.10,203.0.113.11' });
    assert.deepEqual(r.errors, []);
    assert.deepEqual(opts(r), ['--yes', '--agent-only', '--remote-access=0.0.0.0:8443', '--allow-from=203.0.113.10,203.0.113.11']);
});

test('a value that is not what the installer accepts is reported and never copied into the command', () => {
    const evil = ['x; rm -rf /', '$(reboot)', 'a b', "a'b", 'a`b`', 'a\nb', 'a|b', 'a&&b', '../../etc', 'Admin', 'a"b'];
    for (const v of evil) {
        for (const field of ['hostname', 'adminUser', 'adminEmail', 'version', 'commit']) {
            const r = buildCommand({ ...base, [field]: v });
            assert.equal(r.errors.length >= 1, true, `${field}=${JSON.stringify(v)} was accepted`);
            assert.ok(!r.command.includes(v.trim()) || v.trim() === '', `${field}=${JSON.stringify(v)} reached the command`);
        }
        const agent = buildCommand({ ...base, mode: 'agent', remote: v, allow: v });
        assert.ok(!agent.command.includes(v), 'agent field reached the command');
        assert.equal(buildCommand({ ...base, php: [v, '8.3'] }).command.includes(v), false);
    }
});

test('ports must be valid and different', () => {
    assert.equal(buildCommand({ ...base, port: '80' }).errors.length, 1);
    assert.equal(buildCommand({ ...base, port: '70000' }).errors.length, 1);
    assert.equal(buildCommand({ ...base, publicHttps: true, httpsPort: '80' }).errors.length, 1);
    assert.equal(buildCommand({ ...base, publicHttps: true, httpsPort: '8090' }).errors.length, 1, 'the same as the panel port');
    assert.equal(buildCommand({ ...base, publicHttps: true, httpsPort: '9000', port: '9000' }).errors.length, 1);
});

test('at least one PHP version is needed, and old ones are flagged', () => {
    assert.deepEqual(buildCommand({ ...base, php: [] }).errors, ['Choose at least one PHP version.']);
    assert.ok(buildCommand({ ...base, php: ['8.0', '8.3'] }).notes.some((n) => n.includes('PHP 8.0')));
    assert.ok(!buildCommand({ ...base, php: ['8.3'] }).notes.some((n) => n.includes('security fixes')));
});

test('example email domains get a warning, a missing host name gets a note', () => {
    assert.ok(buildCommand({ ...base, adminEmail: 'me@example.com' }).notes.some((n) => n.includes('Let')));
    assert.ok(buildCommand(base).notes.some((n) => n.includes('host name')));
    assert.ok(!buildCommand({ ...base, hostname: 'server.example.com' }).notes.some((n) => n.includes('host name')));
});

test('unknown modes and profiles fall back to the safe defaults', () => {
    const r = buildCommand({ ...base, mode: 'nonsense', profile: 'nonsense' });
    assert.deepEqual(opts(r), ['--yes', '--enable-writes', '--profile=full', '--php=8.3,8.4']);
});

test('the host name is only used with the full profile, and a hidden bad one blocks nothing', () => {
    assert.ok(!buildCommand({ ...base, profile: 'web', hostname: 'server.example.com' }).command.includes('--hostname'));
    assert.deepEqual(buildCommand({ ...base, profile: 'none', hostname: 'not a host!!' }).errors, []);
    assert.ok(buildCommand({ ...base, profile: 'full', hostname: 'server.example.com' }).command.includes('--hostname=server.example.com'));
});
