<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use PHPUnit\Framework\TestCase;
use NimVor\Services\ModSecurityService;

final class ModSecurityRulesTest extends TestCase
{
    public function testSplitAndJoin(): void
    {
        $file = "# block xmlrpc\nSecRule REQUEST_URI \"@beginsWith /xmlrpc.php\" \"id:10001,phase:1,deny,status:403,log,msg:'xmlrpc blocked'\"\n\n"
            . "SecRule ARGS \"@rx foo\" \\\n    \"id:10002,phase:2,pass,log\"\nSecRuleRemoveById 942100\n";
        $rules = ModSecurityService::splitRules($file);
        self::assertCount(3, $rules);
        self::assertSame([10001, '1', 'deny', 'xmlrpc blocked', 'block xmlrpc'], [$rules[0]['id'], $rules[0]['phase'], $rules[0]['action'], $rules[0]['msg'], $rules[0]['comment']]);
        self::assertStringContainsString("\\\n", $rules[1]['text'], 'a continued rule stays one rule');
        self::assertNull($rules[2]['id']);
        // Joining and splitting again gives the same rules.
        self::assertSame($rules, ModSecurityService::splitRules(ModSecurityService::joinRules($rules)));
        self::assertSame(10003, ModSecurityService::nextRuleId($rules));
        self::assertSame(10001, ModSecurityService::nextRuleId([]));
    }
}
