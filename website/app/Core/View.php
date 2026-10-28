<?php

declare(strict_types=1);

namespace NimVorSite\Core;

/** Plain PHP templates with one layout; every value printed goes through e(). */
final class View
{
    private ?string $layout = null;
    /** @var array<string, string> */
    private array $meta = [];
    /** @var (callable(): string)|null makes the CSRF token only when a form needs it */
    private $csrfProvider = null;

    /** @param array<string, mixed> $shared */
    public function __construct(private readonly string $dir, private readonly string $publicDir, private array $shared = [])
    {
    }

    /** @param array<string, mixed> $data */
    public function render(string $name, array $data = []): string
    {
        $this->layout = null;
        $out = $this->include($name, $data);
        if ($this->layout !== null) {
            $layout = $this->layout;
            $this->layout = null;
            return $this->include($layout, ['content' => $out]);
        }
        return $out;
    }

    /** @param array<string, mixed> $data */
    public function partial(string $name, array $data = []): string
    {
        return $this->include($name, $data);
    }

    /** @param array<string, mixed> $data */
    private function include(string $name, array $data): string
    {
        $file = $this->dir . '/' . $name . '.php';
        if (!is_file($file) || str_contains($name, '..')) {
            throw new \RuntimeException('Unknown template ' . $name);
        }
        extract($data, EXTR_SKIP);
        extract($this->shared, EXTR_SKIP); // the page's own values win over the shared defaults
        ob_start();
        try {
            include $file;
            return (string) ob_get_clean();
        } catch (\Throwable $e) {
            ob_end_clean();
            throw $e;
        }
    }

    public function layout(string $name): void
    {
        $this->layout = $name;
    }

    public function set(string $key, string $value): void
    {
        $this->meta[$key] = $value;
    }

    public function meta(string $key, string $default = ''): string
    {
        return $this->meta[$key] ?? $default;
    }

    /** @param callable(): string $provider */
    public function setCsrfProvider(callable $provider): void
    {
        $this->csrfProvider = $provider;
    }

    public function share(string $key, mixed $value): void
    {
        $this->shared[$key] = $value;
    }

    public function e(mixed $v): string
    {
        return e($v);
    }

    /** A versioned URL for a file under public/assets, so a changed file is never served stale. */
    public function asset(string $path): string
    {
        $file = $this->publicDir . '/assets/' . ltrim($path, '/');
        $v = is_file($file) ? substr((string) md5_file($file), 0, 8) : '0';
        return '/assets/' . ltrim($path, '/') . '?v=' . $v;
    }

    public function active(string $prefix): string
    {
        $p = (string) ($this->shared['path'] ?? '/');
        $on = $prefix === '/' ? $p === '/' : ($p === $prefix || str_starts_with($p, rtrim($prefix, '/') . '/'));
        return $on ? ' aria-current="page"' : '';
    }

    public function csrf(): string
    {
        return '<input type="hidden" name="_token" value="' . e($this->csrfProvider === null ? '' : ($this->csrfProvider)()) . '">';
    }

    /** @param array<string, string> $errors */
    public function error(array $errors, string $field): string
    {
        return isset($errors[$field]) ? '<div class="field-error" role="alert">' . e($errors[$field]) . '</div>' : '';
    }

    /**
     * A bar chart as inline SVG (attributes only, so the Content-Security-Policy needs no
     * style exception). Each bar has a title that browsers show on hover.
     *
     * @param array<string, int> $series label => value, in order
     */
    public function barChart(array $series, string $unit): string
    {
        $w = 600;
        $h = 160;
        $n = max(1, count($series));
        $max = max(1, max($series ?: [0]));
        $slot = $w / $n;
        $bar = max(2.0, $slot - 4);
        $out = '<svg class="chart" viewBox="0 0 ' . $w . ' ' . $h . '" role="img" aria-label="' . e($unit) . ' per day" preserveAspectRatio="none">';
        $i = 0;
        foreach ($series as $label => $value) {
            $bh = $value === 0 ? 2.0 : max(3.0, ($h - 8) * $value / $max);
            $out .= '<rect x="' . round($i * $slot + 2, 2) . '" y="' . round($h - $bh, 2) . '" width="' . round($bar, 2) . '" height="' . round($bh, 2) . '" rx="2"><title>' . e($label . ': ' . $value . ' ' . $unit) . '</title></rect>';
            $i++;
        }
        return $out . '</svg>';
    }

    public function icon(string $name, int $size = 22): string
    {
        $paths = [
            'globe' => '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>',
            'server' => '<rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/><path d="M7 7h.01M7 17h.01"/>',
            'lock' => '<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>',
            'mail' => '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="m4 7 8 6 8-6"/>',
            'db' => '<ellipse cx="12" cy="6" rx="7" ry="3"/><path d="M5 6v6c0 1.7 3.1 3 7 3s7-1.3 7-3V6M5 12v6c0 1.7 3.1 3 7 3s7-1.3 7-3v-6"/>',
            'shield' => '<path d="M12 3 5 6v5c0 4.5 3 8 7 10 4-2 7-5.5 7-10V6z"/><path d="m9 12 2 2 4-4"/>',
            'backup' => '<path d="M4 12a8 8 0 1 0 3-6.2"/><path d="M4 4v4h4M12 8v4l3 2"/>',
            'code' => '<path d="m8 8-4 4 4 4M16 8l4 4-4 4M14 5l-4 14"/>',
            'bolt' => '<path d="M13 3 5 14h6l-1 7 8-11h-6z"/>',
            'users' => '<circle cx="9" cy="8" r="3.2"/><path d="M3 20c.6-3.4 3-5 6-5s5.4 1.6 6 5M16 5.5a3 3 0 0 1 0 5.8M18 15c1.8.6 2.7 2 3 4"/>',
            'chart' => '<path d="M4 20V4M4 20h16M8 16v-4M12 16V8M16 16v-6"/>',
            'book' => '<path d="M5 4h10a3 3 0 0 1 3 3v13H8a3 3 0 0 1-3-3z"/><path d="M5 17a3 3 0 0 1 3-3h10"/>',
            'pen' => '<path d="m4 20 1-4L16 5l3 3L8 19z"/><path d="m14 7 3 3"/>',
            'git' => '<circle cx="6" cy="6" r="2.2"/><circle cx="6" cy="18" r="2.2"/><circle cx="18" cy="9" r="2.2"/><path d="M6 8v8M18 11c0 4-6 3-12 5"/>',
            'tool' => '<path d="M14 6a4 4 0 0 0 5 5l-9 9a2.5 2.5 0 0 1-3.5-3.5z"/><path d="M14 6l3-3 4 4-3 3"/>',
            'heart' => '<path d="M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.6A4 4 0 0 1 19 10c0 5.6-7 10-7 10z"/>',
            'info' => '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7.5h.01"/>',
            'chat' => '<path d="M4 5h16v11H9l-5 4z"/>',
            'file' => '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4M9 12h6M9 16h6"/>',
            'check' => '<path d="m5 12 5 5 9-10"/>',
            'arrow' => '<path d="M5 12h14M13 6l6 6-6 6"/>',
            'copy' => '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>',
            'menu' => '<path d="M4 7h16M4 12h16M4 17h16"/>',
            'cloud' => '<path d="M7 18a4 4 0 0 1-.6-7.9A6 6 0 0 1 18 9.5 4.3 4.3 0 0 1 17.5 18z"/>',
        ];
        $p = $paths[$name] ?? '';
        return '<svg class="icon" width="' . $size . '" height="' . $size . '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">' . $p . '</svg>';
    }
}
