<?php

declare(strict_types=1);

namespace NimVor\Dashboard;

/** One dashboard widget as the registry knows it. */
final class Widget
{
    /** Width classes: the dashboard grid (nimvor.css, .nim-widgets) lays them out by the width of the page content, not of the window. */
    public const SIZES = ['sm' => 'nim-span-sm', 'md' => 'nim-span-md', 'lg' => 'nim-span-lg', 'full' => 'nim-span-full'];

    /**
     * @param string $permission what the viewer needs to see the widget at all
     * @param string $size       default size: sm, md, lg or full
     * @param int    $refresh    seconds between automatic refreshes in the page (0: only on request)
     * @param int    $ttl        seconds a measurement is kept before it is measured again
     * @param bool   $inline     cheap (the panel's own records): drawn with the page; otherwise loaded after it
     */
    public function __construct(
        public readonly string $id,
        public readonly string $title,
        public readonly string $icon,
        public readonly string $category,
        public readonly string $permission,
        public readonly string $size = 'md',
        public readonly int $refresh = 0,
        public readonly int $ttl = 30,
        public readonly bool $inline = false,
        public readonly bool $defaultVisible = true,
        public readonly string $description = '',
        public readonly string $linkRoute = '',
        public readonly string $linkLabel = '',
    ) {
    }
}
