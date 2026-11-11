<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Repositories\UserRepository;
use NimVor\Support\ValidationException;
use NimVor\View\Themes;

/**
 * Decides which theme, light/dark mode and accent a signed-in user sees, and saves their choices.
 *
 * Themes apply to hosting customers (the accounts that get the simple layout). Administrators and resellers keep the
 * server-management layout; the mode and accent apply to them as well.
 */
final class ThemeService
{
    public function __construct(
        private readonly SettingsService $settings,
        private readonly UserRepository $users,
        private readonly AuditLogger $audit,
        private readonly ?\NimVor\Repositories\WebsiteRepository $websites = null,
        private readonly ?ServerContext $server = null,
        private readonly ?PlanLimits $limits = null,
    ) {
    }

    /** @return list<string> the themes customers may use; NimVor Modern is always one */
    public function enabled(): array
    {
        $out = [];
        foreach (array_keys(Themes::all()) as $key) {
            if ($key === Themes::DEFAULT || $this->settings->bool('theme_enabled_' . $key)) {
                $out[] = $key;
            }
        }
        return $out;
    }

    /** @return array<string, array{0: string, 1: string}> the accent colours customers may pick */
    public function accents(): array
    {
        return array_filter(Themes::accents(), fn (string $key): bool => $this->settings->bool('theme_accent_' . $key), ARRAY_FILTER_USE_KEY);
    }

    public function customersChoose(): bool
    {
        return $this->settings->bool('theme_customers_choose');
    }

    public function defaultTheme(): string
    {
        $key = $this->settings->string('theme_default');
        return in_array($key, $this->enabled(), true) ? $key : Themes::DEFAULT;
    }

    /**
     * What to render for this viewer.
     *
     * @param array<string, mixed>|null $user
     * @param string|null $preview a theme being tried out in this session only
     * @return array{key: string, label: string, mode: string, accent: string, layout: string, css: ?string, preview: bool, customer: bool}
     */
    public function resolve(?array $user, bool $customer, ?string $preview = null): array
    {
        $enabled = $this->enabled();
        $key = $this->defaultTheme();
        $previewing = false;
        if ($customer && $this->customersChoose()) {
            $own = (string) ($user['ui_theme'] ?? '');
            if (in_array($own, $enabled, true)) {
                $key = $own;
            }
            if ($preview !== null && in_array($preview, $enabled, true)) {
                $key = $preview;
                $previewing = $preview !== $own;
            }
        }
        if (!$customer) {
            $key = Themes::DEFAULT;
        }
        $mode = (string) ($user['ui_mode'] ?? '');
        if (!isset(Themes::modes()[$mode])) {
            $mode = $this->settings->string('theme_default_mode');
        }
        $allowed = $this->accents();
        $accent = (string) ($user['ui_accent'] ?? '');
        if (!isset($allowed[$accent])) {
            $accent = $this->settings->string('theme_default_accent');
            if (!isset($allowed[$accent])) {
                $accent = '';
            }
        }
        // The theme's own colour needs no extra stylesheet.
        if ($accent === Themes::all()[$key]['accent']) {
            $accent = '';
        }
        return [
            'key' => $key,
            'label' => Themes::all()[$key]['label'],
            'mode' => $mode,
            'accent' => $accent,
            'layout' => Themes::layout($key),
            'css' => $key === Themes::DEFAULT ? null : 'assets/css/themes/' . $key . '.css',
            'preview' => $previewing,
            'customer' => $customer,
        ];
    }

    /**
     * Save a user's own choices. An empty value goes back to the administrator's default.
     *
     * @param array<string, mixed> $input
     */
    public function savePreferences(int $userId, array $input): void
    {
        $user = $this->users->find($userId) ?? [];
        $theme = is_scalar($input['theme'] ?? null) ? (string) $input['theme'] : (string) ($user['ui_theme'] ?? '');
        $mode = is_scalar($input['mode'] ?? null) ? (string) $input['mode'] : (string) ($user['ui_mode'] ?? '');
        $accent = is_scalar($input['accent'] ?? null) ? (string) $input['accent'] : (string) ($user['ui_accent'] ?? '');
        $errors = [];
        if ($theme !== '' && (!$this->customersChoose() || !in_array($theme, $this->enabled(), true))) {
            // A customer who may not choose keeps whatever the administrator set.
            $theme = $this->customersChoose() ? '' : (string) ($user['ui_theme'] ?? '');
            $errors['theme'] = 'That theme is not available.';
        }
        if ($mode !== '' && !isset(Themes::modes()[$mode])) {
            $errors['mode'] = 'Choose light, dark or match my device.';
        }
        if ($accent !== '' && !isset($this->accents()[$accent])) {
            $errors['accent'] = 'That colour is not available.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $this->users->setUiPreferences($userId, $theme, $mode, $accent);
        $this->audit->record('appearance.update', 'user', (string) $userId, AuditLogger::SUCCESS, ['theme' => $theme, 'mode' => $mode, 'accent' => $accent]);
    }

    public function resetPreferences(int $userId): void
    {
        $this->users->setUiPreferences($userId, '', '', '');
        $this->audit->record('appearance.reset', 'user', (string) $userId);
    }

    /**
     * What the themes show about the account around every page: the websites and the plan's limits. The pages
     * themselves load their own data; this is only the frame (site switcher, usage meters).
     *
     * @param array<string, mixed> $user
     * @return array{sites: list<array<string, mixed>>, plan: string, usage: list<array<string, mixed>>}
     */
    public function account(array $user): array
    {
        $out = ['sites' => [], 'plan' => '', 'usage' => []];
        if ($this->websites === null || $this->server === null || $this->limits === null) {
            return $out;
        }
        try {
            $out['sites'] = $this->websites->all($this->server->id(), $user);
            $plan = $this->limits->planFor((int) $user['id']);
            if ($plan !== null) {
                $out['plan'] = (string) $plan['name'];
                $out['usage'] = $this->limits->usage((int) $user['id'], []);
            }
        } catch (\Throwable) {
            // The frame shows without the numbers rather than failing the page.
        }
        return $out;
    }
}
