<?php

declare(strict_types=1);

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Plugins\PluginContext;

/*
 * Example NimVor plugin. plugin.php returns a function that receives the
 * plugin context; everything the plugin adds goes through it.
 */
return function (PluginContext $ctx): void {
    $ctx->route('GET', '/', function (Request $request) use ($ctx): Response {
        $info = null;
        try {
            $info = $ctx->agent()->call('system.info');
        } catch (\NimVor\Agent\AgentException) {
            // The page still renders without live data.
        }
        return $ctx->render('index', [
            'info' => $info,
            'note' => $ctx->setting('note', ''),
            'websites' => (int) $ctx->db()->scalar('SELECT COUNT(*) FROM websites'),
        ]);
    }, 'plugin.example-status.view');

    $ctx->route('POST', '/note', function (Request $request) use ($ctx): Response {
        $note = mb_substr(trim((string) $request->input('note', '')), 0, 500);
        $ctx->setSetting('note', $note);
        $ctx->audit('note.update', ['length' => mb_strlen($note)]);
        return Response::redirect('/plugins/example-status');
    }, 'plugin.example-status.edit');

    $ctx->nav('Status note', 'sticky', '/', 'plugin.example-status.view');

    // Plugins can react to panel events.
    $ctx->on('website.created', function (array $payload) use ($ctx): void {
        $ctx->setSetting('last_website', (string) ($payload['website']['domain'] ?? ''));
    });
};
