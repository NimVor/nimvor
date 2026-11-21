<?php

declare(strict_types=1);

namespace NimVor\Dashboard;

/** One dashboard widget as the registry knows it. */
final class Widget
{
    public const SIZES = ['sm' => 'col-12 col-md-6 col-xl-3', 'md' => 'col-12 col-lg-6 col-xl-4', 'lg' => 'col-12 col-xl-8', 'full' => 'col-12'];

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
