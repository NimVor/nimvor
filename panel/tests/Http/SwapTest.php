<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Tests\Support\HttpTestCase;

/** Server → Swap: shows the swap in use and asks the agent to create, resize or remove the swap file. */
final class SwapTest extends HttpTestCase
{
    private const STATUS = [
        'entries' => [['path' => '/swap.img', 'type' => 'file', 'size_bytes' => 2147483648, 'used_bytes' => 1048576, 'priority' => -2, 'managed' => true]],
        'mem_total_bytes' => 4294967296, 'mem_available_bytes' => 2147483648, 'swap_total_bytes' => 2147483648, 'swap_used_bytes' => 1048576,
        'swappiness' => 60, 'fs_type' => 'ext4', 'fs_free_bytes' => 40000000000, 'target' => '/swap.img', 'in_fstab' => true,
        'supported' => true, 'reason' => '', 'max_mb' => 38000, 'min_mb' => 256, 'simulated' => false,
    ];

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agent->succeed('swap.status', self::STATUS);
    }

    public function testThePageShowsTheSwapAreasAndTheControls(): void
    {
        $page = $this->request('GET', '/server/swap')->body();
        self::assertStringContainsString('/swap.img', $page);
        self::assertStringContainsString('Change the size of /swap.img', $page);
        self::assertStringContainsString('Remove the swap file', $page);
        self::assertStringContainsString('<option value="8">8 GB</option>', $page);
    }

    public function testAFileSystemThatCannotHoldSwapShowsWhyAndNoForm(): void
    {
        $this->agent->succeed('swap.status', ['supported' => false, 'reason' => 'A swap file cannot be made on a btrfs file system here.'] + self::STATUS);
        $page = $this->request('GET', '/server/swap')->body();
        self::assertStringContainsString('cannot be made on a btrfs file system', $page);
        self::assertStringNotContainsString('name="size_gb"', $page);
    }

    public function testChoosingAPresetAsksTheAgentForThatManyMegabytes(): void
    {
        $this->agent->succeed('swap.set', self::STATUS);
        $this->post('/server/swap', ['size_gb' => '4']);
        self::assertSame(['size_mb' => 4096], $this->lastCall('swap.set'));
        self::assertStringContainsString('Swap is now', (string) $this->flash('success'));
    }

    public function testACustomSizeWinsOverThePresetAndJunkIsRefusedBeforeTheAgent(): void
    {
        $this->agent->succeed('swap.set', self::STATUS);
        $this->post('/server/swap', ['size_gb' => '4', 'custom_mb' => '1500']);
        self::assertSame(['size_mb' => 1500], $this->lastCall('swap.set'));
        $before = $this->calls('swap.set');
        foreach ([['custom_mb' => 'abc'], ['custom_mb' => '-5'], ['size_gb' => '0'], []] as $bad) {
            $this->post('/server/swap', $bad);
            self::assertNotNull($this->flash('error'));
        }
        self::assertSame($before, $this->calls('swap.set'));
    }

    public function testAnAgentRefusalIsShown(): void
    {
        $this->agent->fail('swap.set', 502, 'failed', 'There is not enough free memory to turn the current swap off safely.');
        $this->post('/server/swap', ['size_gb' => '2']);
        self::assertStringContainsString('not enough free memory', (string) $this->flash('error'));
    }

    public function testRemovingAsksTheAgent(): void
    {
        $this->agent->succeed('swap.remove', self::STATUS);
        $this->post('/server/swap/remove', []);
        self::assertSame(1, $this->calls('swap.remove'));
        self::assertStringContainsString('deleted', (string) $this->flash('success'));
    }

    public function testTheMemoryCardLinksToThePage(): void
    {
        $this->agent->succeed('system.live', ['cpu' => ['per_core' => []], 'memory' => ['total_bytes' => 4294967296, 'used_bytes' => 1, 'available_bytes' => 1, 'used_percent' => 10.0, 'swap_total_bytes' => 0, 'swap_used_bytes' => 0], 'load' => [], 'top_cpu' => [], 'top_memory' => []]);
        $html = json_decode($this->request('GET', '/dashboard/popover/mem')->body(), true)['html'];
        self::assertStringContainsString('href="/server/swap"', $html);
        self::assertStringContainsString('Create swap', $html);
    }
}
