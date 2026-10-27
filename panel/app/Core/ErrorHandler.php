<?php

declare(strict_types=1);

namespace WhaCP\Core;

use Throwable;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Support\Logger;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/**
 * Converts exceptions into safe responses.
 *
 * Users see a short explanation and a reference ID. The exception, file,
 * line and trace are written to the panel log only.
 */
final class ErrorHandler
{
    public function __construct(
        private readonly Logger $logger,
        private readonly ?View $view,
        private readonly bool $debug = false,
    ) {
    }

    public function handle(Throwable $e, ?Request $request): Response
    {
        $reference = strtoupper(bin2hex(random_bytes(4)));
        $wantsJson = $request?->wantsJson() ?? false;

        if ($e instanceof UserFacingException) {
            $this->logger->warning('user-facing error', [
                'reference' => $reference,
                'message' => $e->getMessage(),
                'reason' => $e->reason(),
                'path' => $request?->path(),
                'previous' => $e->getPrevious(),
            ]);
            $payload = $e->toArray() + ['reference' => $reference];
            $status = $e->status();
        } else {
            $this->logger->error('unhandled exception', [
                'reference' => $reference,
                'class' => $e::class,
                'message' => $e->getMessage(),
                'file' => $e->getFile() . ':' . $e->getLine(),
                'trace' => array_slice(explode("\n", $e->getTraceAsString()), 0, 15),
                'path' => $request?->path(),
            ]);
            $payload = [
                'message' => 'Something went wrong while processing your request.',
                'reason' => 'An unexpected internal error occurred.',
                'action' => 'Try again. If the problem persists, give the reference ' . $reference . ' to your administrator.',
                'reference' => $reference,
            ];
            if ($this->debug) {
                $payload['debug'] = $e::class . ': ' . $e->getMessage() . ' @ ' . $e->getFile() . ':' . $e->getLine();
            }
            $status = 500;
        }

        if ($wantsJson) {
            return Response::json(['success' => false, 'error' => $payload], $status);
        }
        if ($this->view !== null) {
            try {
                return Response::html($this->view->render('errors/error', [
                    'title' => $status >= 500 ? 'Unexpected error' : 'Request could not be completed',
                    'message' => $payload['message'],
                    'reason' => $payload['reason'],
                    'remedy' => $payload['action'],
                    'reference' => $reference,
                    'debug' => $payload['debug'] ?? null,
                    'status' => $status,
                ], 'layouts/minimal'), $status);
            } catch (Throwable $renderError) {
                $this->logger->error('error page rendering failed', ['exception' => $renderError]);
            }
        }
        return Response::html(
            '<!doctype html><title>Error</title><h1>Something went wrong</h1><p>Reference: ' . e($reference) . '</p>',
            $status,
        );
    }
}
