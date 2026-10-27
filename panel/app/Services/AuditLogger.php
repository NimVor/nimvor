<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Http\Request;
use WhaCP\Repositories\AuditLogRepository;
use WhaCP\Support\Logger;
use WhaCP\Support\Redactor;

/**
 * Records security-relevant and state-changing actions.
 *
 * The request context (IP, user agent) is attached by middleware; the actor
 * defaults to the authenticated user set via setActor().
 */
class AuditLogger
{
    public const SUCCESS = 'success';
    public const FAILURE = 'failure';
    public const DENIED = 'denied';

    private string $ip = '';
    private string $userAgent = '';
    private ?int $actorId = null;
    private string $actorName = '';

    public function __construct(
        private readonly AuditLogRepository $repository,
        private readonly Logger $logger,
    ) {
    }

    /** @param list<string> $trustedProxies */
    public function setRequest(Request $request, array $trustedProxies = []): void
    {
        $this->ip = $request->ip($trustedProxies);
        $this->userAgent = $request->userAgent();
    }

    public function setActor(?int $id, string $name): void
    {
        $this->actorId = $id;
        $this->actorName = $name;
    }

    /**
     * @param array<string, mixed> $details
     */
    public function record(
        string $action,
        string $targetType = '',
        string $targetId = '',
        string $result = self::SUCCESS,
        array $details = [],
        ?int $userId = null,
        ?string $username = null,
    ): void {
        $row = [
            'user_id' => $userId ?? $this->actorId,
            'username' => mb_substr($username ?? $this->actorName, 0, 190),
            'action' => $action,
            'target_type' => $targetType,
            'target_id' => mb_substr($targetId, 0, 255),
            'result' => $result,
            'ip' => $this->ip,
            'user_agent' => $this->userAgent,
            'details' => json_encode(Redactor::redact($details), JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_PARTIAL_OUTPUT_ON_ERROR) ?: '{}',
            'created_at' => gmdate('c'),
        ];
        try {
            $this->repository->insert($row);
        } catch (\Throwable $e) {
            // Audit failures must never be silent.
            $this->logger->error('audit log write failed', ['action' => $action, 'exception' => $e]);
        }
        $this->logger->info('audit', ['action' => $action, 'target' => $targetType . ':' . $targetId, 'result' => $result, 'user' => $row['username'], 'ip' => $this->ip]);
    }
}
