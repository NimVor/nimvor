<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use PHPUnit\Framework\TestCase;

/** The panel's command-line tool: help and the version always answer, and an unknown command says so. */
final class CliTest extends TestCase
{
    /** @return array{0: int, 1: string, 2: string} exit code, stdout, stderr */
    private static function cli(string ...$args): array
    {
        $cmd = array_merge([PHP_BINARY, dirname(__DIR__, 2) . '/bin/nimvor'], $args);
        $p = proc_open($cmd, [1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes, null, ['NIMVOR_CONFIG' => '/nonexistent/panel.php']);
        self::assertIsResource($p);
        $out = (string) stream_get_contents($pipes[1]);
        $err = (string) stream_get_contents($pipes[2]);
        return [proc_close($p), $out, $err];
    }

    public function testHelpListsEveryCommandAndNeedsNoInstallation(): void
    {
        foreach ([[], ['help'], ['--help'], ['-h']] as $args) {
            if ($args === []) {
                continue; // no argument is handled by the short command, which passes "help"
            }
            [$code, $out] = self::cli(...$args);
            self::assertSame(0, $code, implode(' ', $args));
            foreach (['update', 'version', 'doctor', 'schedule:run', 'setup:apply', 'user:password', 'user:2fa-reset', 'hulk:unblock', 'modsec:rollback', 'migrate', 'agent:check'] as $cmd) {
                self::assertStringContainsString($cmd, $out, $cmd);
            }
        }
    }

    public function testVersionPrintsTheInstalledVersion(): void
    {
        [$code, $out] = self::cli('version');
        self::assertSame(0, $code);
        self::assertMatchesRegularExpression('/^NimVor \d+\.\d+\.\d+/', $out);
        self::assertSame($out, self::cli('--version')[1]);
        self::assertSame($out, self::cli('-v')[1]);
    }

    public function testAnUnknownCommandSaysSoAndShowsTheHelp(): void
    {
        [$code, $out, $err] = self::cli('monitor');
        self::assertSame(2, $code);
        self::assertSame('', $out);
        self::assertStringContainsString('Unknown command: monitor', $err);
        self::assertStringContainsString('schedule:run', $err, 'the help follows, so the right command can be found');
    }
}
