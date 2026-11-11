<?php

declare(strict_types=1);

namespace NimVor\View;

/**
 * The customer panel's themes: what exists, what each one is called, and the settings that control them.
 *
 * A theme is a layout (the frame around every page), a stylesheet, and optionally replacements for a few pages
 * (the websites list). Every theme renders the same pages from the same controllers, so a theme can change how
 * the panel looks and how its navigation is arranged, never what it can do. Adding a theme is described in
 * docs/development/themes.md.
 */
final class Themes
{
    public const DEFAULT = 'modern';

    /** The colour accents a customer may pick; a theme has its own when none is chosen. */
    private const ACCENTS = [
        'teal' => ['Teal', '#0f766e'],
        'blue' => ['Blue', '#2563eb'],
        'violet' => ['Violet', '#7c3aed'],
        'rose' => ['Rose', '#be123c'],
        'amber' => ['Amber', '#b45309'],
        'slate' => ['Slate', '#475569'],
    ];

    private const MODES = ['auto' => 'Match my device', 'light' => 'Light', 'dark' => 'Dark'];

    /**
     * key => label, one line, description, accent the theme looks best in.
     *
     * @return array<string, array{label: string, tagline: string, about: string, accent: string}>
     */
    public static function all(): array
    {
        return [
            'modern' => [
                'label' => 'NimVor Modern',
                'tagline' => 'The standard NimVor panel',
                'about' => 'Website cards, a clear page header and a tool menu for each website. The default for new installations.',
                'accent' => 'teal',
            ],
            'classic' => [
                'label' => 'Classic',
                'tagline' => 'Tools in a grid, grouped by category',
                'about' => 'A familiar hosting-panel layout: search for a tool, or open one from the Files, Databases, Domains, Email, Security, Software and Advanced groups. Your account and usage sit at the side.',
                'accent' => 'amber',
            ],
            'workspace' => [
                'label' => 'Workspace',
                'tagline' => 'Websites first, with a sidebar',
                'about' => 'A sidebar and one card per website with its status, SSL, PHP version and the tools you use most. Suited to agencies and people with many websites.',
                'accent' => 'blue',
            ],
            'simple' => [
                'label' => 'Simple',
                'tagline' => 'Light, quick and easy on a phone',
                'about' => 'A minimal layout with large shortcuts to files, email, databases, domains, SSL and backups, and a tab bar on small screens. Made for ordinary shared hosting.',
                'accent' => 'violet',
            ],
            'compact' => [
                'label' => 'Compact',
                'tagline' => 'Dense lists for people who know their way around',
                'about' => 'Small, tightly packed lists and a category menu, with your account limits always in view. Fewer clicks for experienced users.',
                'accent' => 'slate',
            ],
        ];
    }

    /** @return array<string, array{0: string, 1: string}> key => [label, colour] */
    public static function accents(): array
    {
        return self::ACCENTS;
    }

    /** @return array<string, string> */
    public static function modes(): array
    {
        return self::MODES;
    }

    public static function exists(string $key): bool
    {
        return isset(self::all()[$key]);
    }

    /** The layout template of a theme; the Modern theme keeps the panel's own layout. */
    public static function layout(string $key): string
    {
        return $key === self::DEFAULT ? 'layouts/app' : 'themes/' . $key . '/layout';
    }

    /**
     * The settings the administrator controls (Settings → Customer panel themes).
     *
     * @return array<string, array<string, mixed>>
     */
    public static function settingDefinitions(): array
    {
        $themes = [];
        foreach (self::all() as $key => $t) {
            $themes[$key] = $t['label'];
        }
        $accents = ['' => 'The theme\'s own colour'];
        foreach (self::ACCENTS as $key => [$label]) {
            $accents[$key] = $label;
        }
        $defs = [
            'theme_default' => [
                'category' => 'customer_themes', 'type' => 'string', 'default' => self::DEFAULT, 'options' => $themes,
                'label' => 'Default theme', 'help' => 'Used for customers who have not chosen one. It must be enabled; if it is not, NimVor Modern is used.',
            ],
            'theme_customers_choose' => [
                'category' => 'customer_themes', 'type' => 'bool', 'default' => true,
                'label' => 'Customers may choose their theme', 'help' => 'Off: every customer sees the default theme. Light or dark mode and the accent colour can still be chosen.',
            ],
            'theme_default_mode' => [
                'category' => 'customer_themes', 'type' => 'string', 'default' => 'auto', 'options' => self::MODES,
                'label' => 'Default appearance', 'help' => 'Light, dark, or follow the visitor\'s device. A customer\'s own choice wins.',
            ],
            'theme_default_accent' => [
                'category' => 'customer_themes', 'type' => 'string', 'default' => '', 'options' => $accents,
                'label' => 'Default accent colour', 'help' => 'Used for customers who have not chosen one. It must be one of the colours enabled below.',
            ],
        ];
        foreach (self::all() as $key => $t) {
            if ($key !== self::DEFAULT) {
                $defs['theme_enabled_' . $key] = [
                    'category' => 'customer_themes', 'type' => 'bool', 'default' => true,
                    'label' => $t['label'] . ' theme', 'help' => 'Offer this theme to customers. Customers who chose it fall back to the default theme while it is off.',
                ];
            }
        }
        foreach (self::ACCENTS as $key => [$label]) {
            $defs['theme_accent_' . $key] = [
                'category' => 'customer_themes', 'type' => 'bool', 'default' => true,
                'label' => $label . ' accent', 'help' => 'Let customers pick this accent colour.',
            ];
        }
        return $defs;
    }
}
