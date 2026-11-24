<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $op */
/** @var \NimVorSite\Support\ApiSchema $schema */
/** @var \Closure(string): string $markdown */
$params = [];
foreach ((array) ($op['parameters'] ?? []) as $p) {
    $params[] = $schema->resolve((array) $p);
}
$body = $op['requestBody']['content']['application/json'] ?? null;
$bodyRows = $body !== null ? $schema->rows((array) ($body['schema'] ?? [])) : [];
$id = (string) $op['operationId'];
?>
<section class="api-op" id="op-<?= e($id) ?>">
    <h2 class="api-title"><span class="api-m m-<?= e(strtolower((string) $op['method'])) ?>"><?= e((string) $op['method']) ?></span> <code><?= e((string) $op['path']) ?></code></h2>
    <p class="api-sum"><strong><?= e((string) ($op['summary'] ?? '')) ?></strong> <a class="anchor" href="#op-<?= e($id) ?>" aria-label="Link to this endpoint">#</a></p>
    <?php if (($op['description'] ?? '') !== ''): ?><div class="prose api-desc"><?= $markdown((string) $op['description']) ?></div><?php endif; ?>
    <p class="api-meta">
        <span>Operation <code><?= e($id) ?></code></span>
        <span>Permission <?= isset($op['x-permission']) ? '<code>' . e((string) $op['x-permission']) . '</code>' : '<em>none beyond a valid token</em>' ?></span>
        <span><?= $op['method'] === 'GET' ? 'Works with read-only tokens' : 'Needs a read/write token' ?></span>
    </p>

    <?php if ($params !== []): ?>
        <h3>Parameters</h3>
        <div class="table-wrap"><table class="api-table">
            <thead><tr><th>Name</th><th>In</th><th>Type</th><th>Description</th></tr></thead>
            <tbody>
            <?php foreach ($params as $p): $ps = (array) ($p['schema'] ?? []); ?>
                <tr>
                    <td><code><?= e((string) $p['name']) ?></code><?= !empty($p['required']) ? ' <span class="api-req">required</span>' : '' ?></td>
                    <td><?= e((string) $p['in']) ?></td>
                    <td><?= e($schema->type($ps)) ?></td>
                    <td><?= e((string) ($p['description'] ?? '')) ?><?php
                        $notes = [];
                        if (isset($ps['enum'])) { $notes[] = 'one of ' . implode(', ', array_map('strval', (array) $ps['enum'])); }
                        if (isset($ps['default'])) { $notes[] = 'default ' . (is_bool($ps['default']) ? ($ps['default'] ? 'true' : 'false') : (string) $ps['default']); }
                        if (isset($ps['maximum'])) { $notes[] = 'max ' . $ps['maximum']; }
                        echo $notes === [] ? '' : ' <span class="api-note">(' . e(implode('; ', $notes)) . ')</span>';
                    ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    <?php endif; ?>

    <?php if ($body !== null): ?>
        <h3>Request body <small>JSON<?= !empty($op['requestBody']['required']) ? '' : ', optional' ?></small></h3>
        <?php if ($bodyRows !== []): ?>
            <div class="table-wrap"><table class="api-table">
                <thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead>
                <tbody>
                <?php foreach ($bodyRows as $r): ?>
                    <tr>
                        <td><code><?= e($r['name']) ?></code><?= $r['required'] ? ' <span class="api-req">required</span>' : '' ?></td>
                        <td><?= e($r['type']) ?></td>
                        <td><?= e($r['description']) ?><?= $r['notes'] !== '' ? ' <span class="api-note">(' . e($r['notes']) . ')</span>' : '' ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table></div>
        <?php endif; ?>
        <?php if (isset($body['example'])): ?><pre class="codeblock"><code class="language-json"><?= e((string) json_encode($body['example'], JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE)) ?></code></pre><?php endif; ?>
    <?php endif; ?>

    <h3>Example</h3>
    <pre class="codeblock"><code class="language-bash"><?= e($schema->curl($op)) ?></code></pre>

    <h3>Responses</h3>
    <?php foreach ((array) $op['responses'] as $status => $res): $res = $schema->resolve((array) $res); $json = $res['content']['application/json'] ?? null; ?>
        <div class="api-res">
            <p class="api-res-h"><span class="api-status s-<?= e(substr((string) $status, 0, 1)) ?>"><?= e((string) $status) ?></span> <?= e((string) ($res['description'] ?? '')) ?><?= isset($res['x-name']) ? ' <a class="api-note" href="/api-reference#errors">Error format</a>' : '' ?></p>
            <?php if ($json !== null && !isset($res['x-name'])):
                $rows = $schema->rows((array) ($json['schema']['properties']['data'] ?? [])); ?>
                <?php if ($rows !== []): ?>
                    <div class="table-wrap"><table class="api-table">
                        <thead><tr><th><code>data</code> field</th><th>Type</th><th>Description</th></tr></thead>
                        <tbody>
                        <?php foreach ($rows as $r): ?>
                            <tr><td><code><?= e($r['name']) ?></code></td><td><?= e($r['type']) ?></td><td><?= e($r['description']) ?><?= $r['notes'] !== '' ? ' <span class="api-note">(' . e($r['notes']) . ')</span>' : '' ?></td></tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table></div>
                <?php endif; ?>
                <?php if (isset($json['example'])): ?><pre class="codeblock"><code class="language-json"><?= e((string) json_encode($json['example'], JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE)) ?></code></pre><?php endif; ?>
            <?php endif; ?>
        </div>
    <?php endforeach; ?>
</section>
