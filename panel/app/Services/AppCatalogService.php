<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Database\Connection;
use NimVor\Support\UserFacingException;

/**
 * The admin-managed application catalog: categories and the applications in
 * them, with per-item enable and customer-visibility flags. Drives the
 * Softaculous-style installer (customer view) and the admin Application
 * Manager. Install mechanics stay in AppInstallerService / the agent.
 */
final class AppCatalogService
{
    public function __construct(
        private readonly Connection $db,
        private readonly SettingsService $settings,
    ) {
    }

    /** Whether the application installer is globally enabled. */
    public function installerEnabled(): bool
    {
        return $this->settings->bool('apps_installer_enabled');
    }

    /** @return list<array<string, mixed>> categories ordered for display */
    public function categories(bool $onlyEnabled = false): array
    {
        $where = $onlyEnabled ? 'WHERE enabled = 1' : '';
        return $this->db->fetchAll("SELECT * FROM app_categories {$where} ORDER BY sort, name");
    }

    /** @return array<string, mixed>|null */
    public function find(string $slug): ?array
    {
        return $this->db->fetch('SELECT * FROM app_catalog WHERE slug = ?', [$slug]);
    }

    /**
     * The categories a customer may install from, each with its visible,
     * enabled applications. Empty when the installer is globally disabled.
     *
     * @return list<array<string, mixed>>
     */
    public function forCustomer(): array
    {
        if (!$this->installerEnabled()) {
            return [];
        }
        return $this->grouped("WHERE a.enabled = 1 AND a.customer_visible = 1 AND a.status = 'enabled'", true);
    }

    /**
     * What this user is offered: customers get the enabled, customer-visible
     * applications; administrators also get the applications in testing, so
     * they can prove them before anyone else sees them.
     *
     * @param array<string, mixed> $actor
     * @return list<array<string, mixed>>
     */
    public function forActor(array $actor): array
    {
        if (!\NimVor\Hosting\Scope::isAll($actor)) {
            return $this->forCustomer();
        }
        if (!$this->installerEnabled()) {
            return [];
        }
        return $this->grouped("WHERE a.enabled = 1 AND ((a.customer_visible = 1 AND a.status = 'enabled') OR a.status = 'testing')", true);
    }

    /**
     * Every category with every application, for the admin manager.
     *
     * @return list<array<string, mixed>>
     */
    public function forAdmin(): array
    {
        return $this->grouped('', false);
    }

    /**
     * @return list<array<string, mixed>> categories each with an 'apps' list
     */
    private function grouped(string $appWhere, bool $onlyEnabledCategories): array
    {
        $cats = $this->categories($onlyEnabledCategories);
        $out = [];
        foreach ($cats as $c) {
            $apps = $this->db->fetchAll(
                "SELECT a.* FROM app_catalog a WHERE a.category_slug = ? " . ($appWhere !== '' ? 'AND ' . substr($appWhere, 6) : '') . " ORDER BY a.sort, a.name",
                [$c['slug']]
            );
            if ($apps === [] && $onlyEnabledCategories) {
                continue; // hide empty categories from customers
            }
            $out[] = $c + ['apps' => $apps];
        }
        return $out;
    }

    /**
     * Whether an actor may install the given app right now.
     *
     * @param array<string, mixed> $actor
     */
    public function installableBy(string $slug, array $actor): bool
    {
        if (!$this->installerEnabled()) {
            return false;
        }
        $app = $this->find($slug);
        if ($app === null || (int) $app['enabled'] !== 1) {
            return false;
        }
        $cat = $this->db->fetch('SELECT enabled FROM app_categories WHERE slug = ?', [$app['category_slug']]);
        if ($cat !== null && (int) $cat['enabled'] !== 1) {
            return false;
        }
        $status = (string) ($app['status'] ?? 'enabled');
        if ($status === 'testing') {
            return \NimVor\Hosting\Scope::isAll($actor); // only administrators may try an application still in testing
        }
        if ($status !== 'enabled') {
            return false; // draft, deprecated and disabled applications are not installed
        }
        if (\NimVor\Hosting\Scope::isAll($actor)) {
            return true; // administrators see everything that is enabled
        }
        return (int) $app['customer_visible'] === 1;
    }

    public function setInstallerEnabled(bool $on, ?int $userId): void
    {
        $this->settings->update(['apps_installer_enabled' => $on ? '1' : '0'], $userId);
    }

    public function setCategoryEnabled(string $slug, bool $on): void
    {
        $this->db->run('UPDATE app_categories SET enabled = ? WHERE slug = ?', [$on ? 1 : 0, $slug]);
    }

    /**
     * @param 'enabled'|'customer_visible' $field
     * @throws UserFacingException when a customer-visible switch is asked for an application that is not proven yet
     */
    public function setAppFlag(string $slug, string $field, bool $on): void
    {
        if (!in_array($field, ['enabled', 'customer_visible'], true)) {
            return;
        }
        $row = $this->find($slug);
        if ($on && $field === 'customer_visible' && $row !== null && (string) ($row['status'] ?? 'enabled') !== 'enabled') {
            throw new UserFacingException('This application is still ' . (string) $row['status'] . ': it cannot be offered to customers until it has passed its real-host installation test.');
        }
        $this->db->run("UPDATE app_catalog SET {$field} = ? WHERE slug = ?", [$on ? 1 : 0, $slug]);
    }
}
