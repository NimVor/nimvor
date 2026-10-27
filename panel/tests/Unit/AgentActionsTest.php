<?php

declare(strict_types=1);

namespace WhaCP\Tests\Unit;

use PHPUnit\Framework\TestCase;

/**
 * Every agent action the panel calls exists in the agent (its generated
 * API description). Tests with a fake agent cannot catch a misnamed action.
 */
final class AgentActionsTest extends TestCase
{
    public function testEveryCalledActionExists(): void
    {
        $spec = dirname(__DIR__, 3) . '/agent/api/openapi.yaml';
        if (!is_file($spec)) {
            self::markTestSkipped('The agent sources are not next to the panel.');
        }
        preg_match_all('/^  ([a-z0-9_.]+): \{ mutating/m', (string) file_get_contents($spec), $m);
        $have = array_flip($m[1]);
        self::assertGreaterThan(100, count($have), 'the action list was read');
        $missing = [];
        $root = dirname(__DIR__, 2);
        $files = new \RecursiveIteratorIterator(new \RecursiveDirectoryIterator($root . '/app', \FilesystemIterator::SKIP_DOTS));
        foreach ([...iterator_to_array($files), new \SplFileInfo($root . '/bin/whacp')] as $f) {
            if (!$f->isFile() || ($f->getExtension() !== 'php' && $f->getFilename() !== 'whacp')) {
                continue;
            }
            preg_match_all('/->call\(\'([a-z0-9_.]+)\'/', (string) file_get_contents($f->getPathname()), $calls);
            foreach ($calls[1] as $action) {
                if (!isset($have[$action])) {
                    $missing[] = $action . ' (' . substr($f->getPathname(), strlen($root) + 1) . ')';
                }
            }
        }
        self::assertSame([], array_values(array_unique($missing)), 'the panel calls actions the agent does not have');
    }
}
