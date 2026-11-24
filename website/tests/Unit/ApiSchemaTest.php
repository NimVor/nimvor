<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use NimVorSite\Support\ApiSchema;
use PHPUnit\Framework\TestCase;

final class ApiSchemaTest extends TestCase
{
    public function testAllOfPartsAreMergedIntoRows(): void
    {
        $spec = ['components' => ['schemas' => ['Zone' => ['type' => 'object', 'required' => ['id'], 'properties' => ['id' => ['type' => 'integer', 'description' => 'The id.'], 'name' => ['type' => 'string']]]]]];
        $s = new ApiSchema($spec);
        $rows = $s->rows(['type' => 'object', 'allOf' => [['$ref' => '#/components/schemas/Zone']], 'properties' => ['warnings' => ['type' => 'array', 'items' => ['type' => 'string']]]]);
        self::assertSame(['id', 'name', 'warnings'], array_column($rows, 'name'));
        self::assertTrue($rows[0]['required']);
        self::assertSame('string[]', $rows[2]['type']);
    }

    public function testNestedObjectsAndArraysGetDottedNames(): void
    {
        $s = new ApiSchema([]);
        $rows = $s->rows(['type' => 'object', 'properties' => ['user' => ['type' => 'object', 'properties' => ['id' => ['type' => 'integer']]], 'items' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => ['n' => ['type' => 'string']]]]]]);
        self::assertSame(['user', 'user.id', 'items', 'items[].n'], array_column($rows, 'name'));
    }

    public function testCurlFillsPathsAndBody(): void
    {
        $s = new ApiSchema([]);
        $cmd = $s->curl(['method' => 'POST', 'path' => '/dns/zones/{id}/records', 'parameters' => [['name' => 'id', 'in' => 'path', 'schema' => ['type' => 'integer']]], 'requestBody' => ['content' => ['application/json' => ['example' => ['type' => 'A']]]]]);
        self::assertStringContainsString('-X POST', $cmd);
        self::assertStringContainsString('/api/v1/dns/zones/7/records', $cmd);
        self::assertStringContainsString("-d '{\"type\":\"A\"}'", $cmd);
    }
}
