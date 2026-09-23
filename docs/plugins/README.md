# Plugins (planned — Phase 23)

This page records the intended design so that current code does not paint
the project into a corner.

## Goals

Plugins can add UI pages, API endpoints, hooks, services, application
installers, monitoring integrations, backup, DNS and cloud providers:

```text
plugins/
    cloudflare/
    backup-s3/
    wordpress/
```

## Principles

- **No implicit root.** Plugins run inside the panel with the panel's
  privileges. Anything privileged must be an agent action the plugin
  *declares*; administrators approve the declared actions on install.
- **Manifest first.** `plugin.json` declares name, version, required WhaCP
  version, permissions it defines, routes, hooks and agent actions it needs.
- **Namespaced.** Routes under `/plugins/<name>/`, permissions prefixed
  `<name>.`, database tables prefixed `plugin_<name>_`, migrations owned by
  the plugin.
- **Auditable.** Plugin actions go through the same audit logger.
- **Removable.** Uninstalling a plugin removes its routes, permissions and
  (optionally) data without affecting the core.

The router, permission catalogue and migrator are already designed so they
can accept plugin-provided entries.
