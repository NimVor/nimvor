<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Agent\AgentClient;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\SecurityService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: the server's firewall. Every endpoint calls SecurityService, as the web interface does. A
 * change is applied under the agent's guard and undone automatically after two minutes unless it is
 * confirmed (`POST /security/confirm`), so a change that cuts off access heals itself. The agent refuses a
 * change that would remove SSH or the panel's own port. These endpoints need the security permissions,
 * which hosting customers do not have.
 */
final class FirewallApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly SecurityService $security,
        private readonly AgentClient $agent,
    ) {
    }

    /**
     * The unconfirmed change, if there is one.
     *
     * @return array<string, mixed>|null
     */
    private function pendingOut(): ?array
    {
        $p = $this->security->pending();
        return $p === null ? null : ['id' => (string) $p['id'], 'summary' => (string) ($p['summary'] ?? ''), 'kind' => (string) ($p['kind'] ?? ''), 'deadline' => $p['deadline'] ?? null];
    }

    /**
     * What a guarded change answers with: the pending change to confirm.
     *
     * @return array<string, mixed>
     */
    private function changed(string $message): array
    {
        $p = $this->pendingOut();
        return ['pending' => $p, 'message' => $p === null ? $message : 'Applied. Confirm it within 2 minutes with POST /security/confirm, otherwise it is undone automatically.'];
    }

    public function status(Request $r): Response
    {
        return $this->guard(function (): Response {
            $fw = (array) $this->agent->call('firewall.status');
            return self::ok([
                'installed' => !empty($fw['installed']), 'active' => !empty($fw['active']), 'backend' => (string) ($fw['backend'] ?? 'ufw'),
                'default_incoming' => $fw['default_incoming'] ?? null, 'default_outgoing' => $fw['default_outgoing'] ?? null,
                'ssh_port' => (int) ($fw['ssh_port'] ?? 22), 'ssh_allowed' => !empty($fw['ssh_allowed']),
                'rules' => array_values(array_map(static fn ($x): array => [
                    'spec' => (string) (((array) $x)['spec'] ?? ''), 'action' => (string) (((array) $x)['action'] ?? ''),
                    'port' => (string) (((array) $x)['port'] ?? ''), 'proto' => (string) (((array) $x)['proto'] ?? ''), 'from' => (string) (((array) $x)['from'] ?? ''),
                    'comment' => (string) (((array) $x)['comment'] ?? ''), 'protected' => ((array) $x)['protected'] ?? null,
                ], (array) ($fw['rules'] ?? []))),
                'pending' => $this->pendingOut(),
            ]);
        });
    }

    public function addRule(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $in = self::body($r);
            $errors = [];
            if (!in_array((string) ($in['action'] ?? 'allow'), ['allow', 'deny', 'reject', 'limit'], true)) {
                $errors['action'] = 'Use allow, deny, reject or limit.';
            }
            if (trim((string) ($in['port'] ?? '')) === '') {
                $errors['port'] = 'Enter a port or range, for example 8080 or 8000:8100.';
            }
            if ($errors !== []) {
                throw new ValidationException($errors);
            }
            return self::ok($this->changed($this->security->addRule($in)), 202);
        });
    }

    public function deleteRule(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $spec = trim((string) ($r->query('spec', self::body($r)['spec'] ?? '')));
            if ($spec === '') {
                throw new ValidationException(['spec' => 'Name the rule to delete, exactly as GET /security/firewall lists it.']);
            }
            return self::ok($this->changed($this->security->deleteRule($spec)), 202);
        });
    }

    public function setEnabled(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $in = self::body($r);
            if (!array_key_exists('enabled', $in) || filter_var($in['enabled'], FILTER_VALIDATE_BOOL, FILTER_NULL_ON_FAILURE) === null) {
                throw new ValidationException(['enabled' => 'Send true or false.']);
            }
            $allowFrom = trim((string) ($in['allow_from'] ?? ''));
            if ($allowFrom !== '' && filter_var($allowFrom, FILTER_VALIDATE_IP) === false) {
                throw new ValidationException(['allow_from' => 'Enter an IP address or leave it out.']);
            }
            $msg = $this->security->setFirewall(filter_var($in['enabled'], FILTER_VALIDATE_BOOL), filter_var($in['allow_services'] ?? true, FILTER_VALIDATE_BOOL), $allowFrom);
            return self::ok($this->changed($msg), 202);
        });
    }

    public function protection(Request $r): Response
    {
        return $this->guard(fn (): Response => self::ok((array) $this->agent->call('firewall.protection')));
    }

    public function setProtection(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $in = self::body($r);
            foreach (['enabled', 'invalid'] as $k) {
                $in[$k] = filter_var($in[$k] ?? false, FILTER_VALIDATE_BOOL) ? '1' : '0';
            }
            foreach (['conn_limits', 'port_flood', 'exempt'] as $k) {
                if (is_array($in[$k] ?? null)) {
                    $in[$k] = implode(' ', array_map('strval', $in[$k]));
                }
            }
            return self::ok($this->changed($this->security->setProtection($in)), 202);
        });
    }

    public function pending(Request $r): Response
    {
        return self::ok(['pending' => $this->pendingOut()]);
    }

    public function confirm(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $this->security->confirm($this->changeId($r));
            return self::ok(['confirmed' => $this->changeId($r)]);
        });
    }

    public function undo(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $this->security->undo($this->changeId($r));
            return self::ok(['undone' => $this->changeId($r)]);
        });
    }

    private function changeId(Request $r): string
    {
        $id = trim((string) (self::body($r)['id'] ?? ''));
        if (!preg_match('/^[a-f0-9]{8,32}$/', $id)) {
            throw new ValidationException(['id' => 'Send the id of the pending change.']);
        }
        return $id;
    }
}
