'use strict';
// Run with: node --test tests/js
const test = require('node:test');
const assert = require('node:assert/strict');
const { buildCommand } = require('../../public/assets/js/install-builder.js');

const base = { mode: 'hosting', profile: 'full', hostname: '', adminUser: '', adminEmail: '', php: ['8.3', '8.4'], remote: '', allow: '' };
const opts = (r) => r.command.split(' \\\n  ').slice(1);
const FIXED = ['--yes', '--resume'];

test('the default hosting command always has --yes, --resume and --public-https', () => {
    const r = buildCommand(base);
    assert.deepEqual(r.errors, []);
    assert.equal(r.command.split('\n')[0], 'curl -fsSL https://nimvor.com/install | sudo bash -s -- \\');
    assert.deepEqual(opts(r), [...FIXED, '--enable-writes', '--profile=full', '--php=8.3,8.4', '--public-https']);
});

test('every choice turns into the option the installer has', () => {
    const r = buildCommand({ ...base, profile: 'full', hostname: 'server.example.com', adminUser: 'boss', adminEmail: 'boss@mydomain.org', php: ['8.4', '8.2', '8.3'] });
    assert.deepEqual(r.errors, []);
    assert.deepEqual(opts(r), [...FIXED, '--enable-writes', '--profile=full', '--hostname=server.example.com', '--php=8.2,8.3,8.4', '--admin-user=boss', '--admin-email=boss@mydomain.org', '--public-https']);
});

test('ports, questions and the other installer options are not offered: they never reach the command', () => {
    const r = buildCommand({ ...base, port: '9000', httpsPort: '9443', publicHttps: false, noThirdParty: true, noDeps: true, forceUnsupported: true, dryRun: true, version: '1.1.0', commit: 'abc1234', yes: false, resume: false });
    const o = opts(r);
    for (const bad of ['--port', '--no-third-party-repos', '--no-install-deps', '--force-unsupported', '--dry-run', '--version', '--commit']) {
        assert.ok(!o.some((x) => x.startsWith(bad)), bad + ' reached the command');
    }
    assert.ok(o.includes('--public-https'), 'always the default HTTPS port');
    assert.ok(o.includes('--yes') && o.includes('--resume'));
});

test('profile none and the look-first mode ask for no PHP and no write mode', () => {
    assert.deepEqual(opts(buildCommand({ ...base, profile: 'none' })), [...FIXED, '--enable-writes', '--profile=none', '--public-https']);
    const look = buildCommand({ ...base, mode: 'look' });
    assert.deepEqual(opts(look), [...FIXED, '--public-https']);
    assert.ok(look.notes.length >= 1);
});

test('agent only needs an address and the panel server, and has no panel HTTPS', () => {
    assert.equal(buildCommand({ ...base, mode: 'agent' }).errors.length, 2);
    const r = buildCommand({ ...base, mode: 'agent', remote: '0.0.0.0:8443', allow: '203.0.113.10,203.0.113.11' });
    assert.deepEqual(r.errors, []);
    assert.deepEqual(opts(r), [...FIXED, '--agent-only', '--remote-access=0.0.0.0:8443', '--allow-from=203.0.113.10,203.0.113.11']);
});

test('a value that is not what the installer accepts is reported and never copied into the command', () => {
    const evil = ['x; rm -rf /', '$(reboot)', 'a b', "a'b", 'a`b`', 'a\nb', 'a|b', 'a&&b', '../../etc', 'Admin', 'a"b'];
    for (const v of evil) {
        for (const field of ['hostname', 'adminUser', 'adminEmail']) {
            const r = buildCommand({ ...base, [field]: v });
            assert.equal(r.errors.length >= 1, true, `${field}=${JSON.stringify(v)} was accepted`);
            assert.ok(!r.command.includes(v.trim()) || v.trim() === '', `${field}=${JSON.stringify(v)} reached the command`);
        }
        const agent = buildCommand({ ...base, mode: 'agent', remote: v, allow: v });
        assert.ok(!agent.command.includes(v), 'agent field reached the command');
        assert.equal(buildCommand({ ...base, php: [v, '8.3'] }).command.includes(v), false);
    }
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
    assert.deepEqual(opts(r), [...FIXED, '--enable-writes', '--profile=full', '--php=8.3,8.4', '--public-https']);
});

test('the host name is only used with the full profile, and a hidden bad one blocks nothing', () => {
    assert.ok(!buildCommand({ ...base, profile: 'web', hostname: 'server.example.com' }).command.includes('--hostname'));
    assert.deepEqual(buildCommand({ ...base, profile: 'none', hostname: 'not a host!!' }).errors, []);
    assert.ok(buildCommand({ ...base, profile: 'full', hostname: 'server.example.com' }).command.includes('--hostname=server.example.com'));
});
