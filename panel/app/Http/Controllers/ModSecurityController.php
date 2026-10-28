<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Permissions as P;
use NimVor\Services\ModSecurityService;
use NimVor\Services\SecurityService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Security → ModSecurity (administrators: rules and every website) and a
 * website's Web firewall page (its owner: on, detection only or off).
 */
final class ModSecurityController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ModSecurityService $modsec,
        private readonly SecurityService $security,
        private readonly WebsiteRepository $websites,
        private readonly ServerContext $server,
        private readonly \NimVor\Security\Authorizer $authorizer,
        private readonly \NimVor\Services\SettingsService $settings,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'ModSecurity rules are managed by server administrators.', 'status' => 403], 403);
    }

    /** @param callable(): string $fn */
    private function act(Request $request, string $to, callable $fn, bool $adminOnly = true): Response
    {
        if ($adminOnly && ($r = $this->denied($request))) {
            return $r;
        }
        try {
            return $this->redirectWithSuccess($to, $fn());
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }

    public function index(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $status = null;
        $error = null;
        $custom = '';
        $events = [];
        try {
            $status = $this->modsec->status();
            $custom = $this->modsec->customRules();
            $events = $this->modsec->events(null, 50);
        } catch (AgentException $e) {
            $error = $e;
        }
        $sites = $this->websites->all($this->server->id(), $this->user($request));
        $installs = [];
        try {
            $installs = $this->modsec->installs();
        } catch (AgentException) {
            // An older agent: no installation records.
        }
        return $this->render('security/modsec', [
            'tab' => 'modsec', 'pending' => $this->security->pending(), 'agentError' => $error,
            'status' => $status, 'custom' => $custom, 'events' => $events, 'sites' => $sites, 'installs' => $installs,
            'retention' => $this->settings->int('modsec_backup_retention_days'),
        ]);
    }

    public function configure(Request $request): Response
    {
        return $this->act($request, '/security/modsec', function () use ($request): string {
            $this->modsec->configure($request->all(), $this->userId($request));
            return $request->input('enabled') === '1' ? 'ModSecurity settings saved and loaded.' : 'ModSecurity is off: no website is filtered.';
        });
    }

    public function saveCustom(Request $request): Response
    {
        return $this->act($request, '/security/modsec#custom', function () use ($request): string {
            $n = $this->modsec->saveCustomRules(str_replace("\r\n", "\n", (string) $request->input('rules', '')), $this->userId($request));
            $on = !empty(($this->modsec->status()['config']['custom']['enabled'] ?? false));
            return $n . ' custom rule(s) saved; Nginx accepted them.' . ($on ? '' : ' Turn "Custom rules" on in the server-wide settings to use them.');
        });
    }

    /** Custom rules one at a time: add, change or delete a rule; the whole set is checked again. */
    public function saveRule(Request $request): Response
    {
        return $this->act($request, '/security/modsec#custom', function () use ($request): string {
            $rules = ModSecurityService::splitRules($this->modsec->customRules());
            $text = trim(str_replace("\r\n", "\n", (string) $request->input('rule', '')));
            $comment = trim(str_replace(["\r\n", "\r"], "\n", mb_substr((string) $request->input('comment', ''), 0, 500)));
            $index = $request->input('index', '');
            $op = $request->string('op');
            $target = null;
            if ($index !== '' && $index !== null) {
                $i = (int) $index;
                // The rule must still be the one the page showed.
                if (!isset($rules[$i]) || !hash_equals(hash('sha256', $rules[$i]['text']), $request->string('sha'))) {
                    throw new \NimVor\Support\UserFacingException('The custom rules changed since the page was loaded. Reload the page and try again.');
                }
                $target = $i;
            }
            if ($op === 'delete') {
                if ($target === null) {
                    throw new \NimVor\Support\UserFacingException('Choose a rule to delete.');
                }
                array_splice($rules, $target, 1);
                $this->modsec->saveCustomRules(ModSecurityService::joinRules($rules), $this->userId($request));
                return 'Rule deleted; Nginx accepted the remaining rules.';
            }
            if ($text === '') {
                throw new \NimVor\Support\UserFacingException('Enter the rule.');
            }
            $new = ['text' => $text, 'comment' => $comment];
            if ($target === null) {
                $rules[] = $new;
            } else {
                $rules[$target] = $new;
            }
            $this->modsec->saveCustomRules(ModSecurityService::joinRules($rules), $this->userId($request));
            $on = !empty(($this->modsec->status()['config']['custom']['enabled'] ?? false));
            return ($target === null ? 'Rule added' : 'Rule saved') . '; Nginx accepted it.' . ($on ? '' : ' Turn "Custom rules" on in the server-wide settings to use them.');
        });
    }

    /** Install or upgrade the Nginx connector (a transaction on the server). */
    public function install(Request $request): Response
    {
        return $this->act($request, '/security/modsec#installs', function () use ($request): string {
            $this->modsec->install($request->all(), $this->userId($request));
            return 'Installing the ModSecurity connector in the background. Everything is backed up first and rolled back if any step fails; you will be notified when it finishes.';
        });
    }

    public function installCrs(Request $request): Response
    {
        return $this->act($request, '/security/modsec#installs', function () use ($request): string {
            $this->modsec->installCrs($this->userId($request));
            return 'Installing the OWASP Core Rule Set in the background. It starts in detection only: websites set to block only log until you allow blocking.';
        });
    }

    public function rollback(Request $request, string $id): Response
    {
        return $this->act($request, '/security/modsec#installs', function () use ($request, $id): string {
            $this->modsec->rollback($id, $this->userId($request));
            return 'Rolling back installation ' . $id . ' in the background. If Nginx would not accept the result, nothing changes.';
        });
    }

    public function update(Request $request): Response
    {
        return $this->act($request, '/security/modsec', function () use ($request): string {
            $this->modsec->update($this->userId($request));
            return 'Updating the rule packages in the background; you will be notified when it finishes.';
        });
    }

    /** Administrators, from the ModSecurity page: mode, vendors, rules. */
    public function saveSite(Request $request, string $id): Response
    {
        return $this->act($request, '/security/modsec#sites', function () use ($request, $id): string {
            $site = $this->websites->find($this->server->id(), (int) $id, $this->user($request)) ?? throw new UserFacingException('That website does not exist.');
            $exclude = array_values(array_diff(['crs', 'custom'], array_map('strval', (array) $request->input('vendors', []))));
            $this->modsec->setSite($site, $request->string('mode'), $exclude, ModSecurityService::parseIds($request->string('disabled_rules')), true, $this->userId($request));
            return 'ModSecurity for ' . $site['domain'] . ' saved.';
        });
    }

    /** A website's Web firewall page. */
    public function site(Request $request, string $id): Response
    {
        $site = $this->websites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $status = null;
        $error = null;
        $events = [];
        try {
            $status = $this->modsec->status();
            $events = $this->modsec->events($site, 50);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/firewall', [
            'site' => $site, 'tab' => 'firewall', 'agentError' => $error, 'status' => $status, 'events' => $events,
            'setting' => $status !== null ? ModSecurityService::siteSetting($status, $site) : null,
            'isAdmin' => Scope::isAll($this->user($request)) && $this->can($request, P::SECURITY_MANAGE),
        ]);
    }

    /** The website's page: owners set the mode; administrators also turn single rules off. */
    public function saveSiteMode(Request $request, string $id): Response
    {
        $to = '/sites/' . (int) $id . '/firewall';
        return $this->act($request, $to, function () use ($request, $id): string {
            $site = $this->websites->find($this->server->id(), (int) $id, $this->user($request)) ?? throw new UserFacingException('That website does not exist.');
            $admin = Scope::isAll($this->user($request)) && $this->can($request, P::SECURITY_MANAGE);
            $disabled = null;
            $mode = $request->string('mode');
            if ($admin && $request->string('disable_rule') !== '') {
                $cur = ModSecurityService::siteSetting($this->modsec->status(), $site);
                $mode = $cur['mode'];
                $disabled = array_merge($cur['disabled_rules'], ModSecurityService::parseIds($request->string('disable_rule')));
            }
            $this->modsec->setSite($site, $mode, null, $disabled, $admin, $this->userId($request));
            return $disabled !== null ? 'Rule ' . $request->string('disable_rule') . ' is now off for ' . $site['domain'] . '.' : 'Web firewall for ' . $site['domain'] . ': ' . strtolower(ModSecurityService::MODES[$mode] ?? $mode) . '.';
        }, false);
    }

    private function can(Request $request, string $permission): bool
    {
        return $this->authorizer->can($this->user($request), $permission);
    }
}
