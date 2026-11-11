<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\SettingsService;
use NimVor\Services\ThemeService;
use NimVor\Support\ValidationException;
use NimVor\View\Themes;
use NimVor\View\View;

/**
 * A user's own appearance (theme, light/dark, accent colour) and the administrator's controls over it.
 */
final class AppearanceController extends Controller
{
    private const PREVIEW = 'ui_theme_preview';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ThemeService $themes,
        private readonly SettingsService $settings,
    ) {
        parent::__construct($view, $session);
    }

    public function show(Request $request): Response
    {
        $user = $this->user($request);
        return $this->render('appearance/show', [
            'profile' => $user,
            'enabled' => $this->themes->enabled(),
            'accents' => $this->themes->accents(),
            'customersChoose' => $this->themes->customersChoose(),
            'defaultTheme' => $this->themes->defaultTheme(),
            'isCustomer' => $this->view->simpleLayout(),
        ]);
    }

    public function save(Request $request): Response
    {
        try {
            $this->themes->savePreferences($this->userId($request), [
                'theme' => $request->input('theme', null),
                'mode' => $request->input('mode', null),
                'accent' => $request->input('accent', null),
            ]);
        } catch (ValidationException $e) {
            return $this->backWithErrors('/appearance', $e, $request->all());
        }
        $this->session->remove(self::PREVIEW);
        return $this->redirectWithSuccess('/appearance', 'Appearance saved.');
    }

    public function reset(Request $request): Response
    {
        $this->themes->resetPreferences($this->userId($request));
        $this->session->remove(self::PREVIEW);
        return $this->redirectWithSuccess('/appearance', 'Appearance restored to the default.');
    }

    /** Try a theme in this session only; nothing is saved until the customer keeps it. */
    public function preview(Request $request): Response
    {
        $key = (string) $request->input('theme', '');
        if (!$this->view->simpleLayout() || !$this->themes->customersChoose() || !in_array($key, $this->themes->enabled(), true)) {
            return $this->redirectWithError('/appearance', 'That theme cannot be previewed.');
        }
        $this->session->set(self::PREVIEW, $key);
        return Response::redirect('/websites');
    }

    public function cancelPreview(Request $request): Response
    {
        $this->session->remove(self::PREVIEW);
        return Response::redirect('/appearance');
    }

    public function admin(Request $request): Response
    {
        return $this->render('appearance/admin', [
            'values' => $this->settings->all(),
            'definitions' => Themes::settingDefinitions(),
        ]);
    }

    public function saveAdmin(Request $request): Response
    {
        $input = array_filter($request->all(), static fn ($v, $k): bool => is_string($k) && str_starts_with($k, 'theme_'), ARRAY_FILTER_USE_BOTH);
        $default = (string) ($input['theme_default'] ?? $this->settings->string('theme_default'));
        $enabled = (string) ($input['theme_enabled_' . $default] ?? ($default === Themes::DEFAULT ? '1' : $this->settings->bool('theme_enabled_' . $default)));
        if ($default !== Themes::DEFAULT && !filter_var($enabled, FILTER_VALIDATE_BOOL)) {
            return $this->backWithErrors('/settings/themes', new ValidationException(['theme_default' => 'The default theme must be one of the enabled themes.']), $request->all());
        }
        $accent = (string) ($input['theme_default_accent'] ?? $this->settings->string('theme_default_accent'));
        if ($accent !== '' && isset($input['theme_accent_' . $accent]) && !filter_var($input['theme_accent_' . $accent], FILTER_VALIDATE_BOOL)) {
            return $this->backWithErrors('/settings/themes', new ValidationException(['theme_default_accent' => 'The default accent must be one of the enabled colours.']), $request->all());
        }
        try {
            $this->settings->update($input, $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/settings/themes', $e, $request->all());
        }
        return $this->redirectWithSuccess('/settings/themes', 'Theme settings saved.');
    }
}
