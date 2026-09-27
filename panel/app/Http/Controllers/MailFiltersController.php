<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\MailFilterService;
use WhaCP\Services\MailService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/** Email filters, autoresponder (per mailbox) and mail routing (per domain). */
final class MailFiltersController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MailService $mail,
        private readonly MailFilterService $filters,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array{0: array<string, mixed>, 1: array<string, mixed>}|null domain and mailbox the user may manage */
    private function target(Request $request, string $id, string $mailbox): ?array
    {
        $domain = $this->mail->domain((int) $id, $this->user($request));
        if ($domain === null) {
            return null;
        }
        try {
            return [$domain, $this->filters->mailbox($domain, (int) $mailbox)];
        } catch (UserFacingException) {
            return null;
        }
    }

    private static function page(array $domain, array $mailbox): string
    {
        return '/mail/domains/' . (int) $domain['id'] . '/mailboxes/' . (int) $mailbox['id'] . '/filters';
    }

    public function show(Request $request, string $id, string $mailbox): Response
    {
        $t = $this->target($request, $id, $mailbox);
        if ($t === null) {
            return $this->render('errors/error', ['title' => 'Mailbox not found', 'message' => 'This mailbox does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        [$domain, $box] = $t;
        $data = null;
        $error = null;
        try {
            $data = $this->filters->get((string) $box['address']);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('mail/filters', ['domain' => $domain, 'mailbox' => $box, 'data' => $data, 'agentError' => $error]);
    }

    /** @param callable(array<string, mixed>, array<string, mixed>, array<string, mixed>): string $fn */
    private function change(Request $request, string $id, string $mailbox, callable $fn): Response
    {
        $t = $this->target($request, $id, $mailbox);
        if ($t === null) {
            return $this->redirectWithError('/mail', 'Mailbox not found.');
        }
        [$domain, $box] = $t;
        $back = self::page($domain, $box);
        try {
            $data = $this->filters->get((string) $box['address']);
            return $this->redirectWithSuccess($back, $fn($domain, $box, $data));
        } catch (ValidationException $e) {
            return $this->redirectWithError($back, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
    }

    public function save(Request $request, string $id, string $mailbox): Response
    {
        return $this->change($request, $id, $mailbox, function (array $domain, array $box, array $data) use ($request): string {
            $filter = MailFilterService::fromForm($request->all());
            $index = $request->input('index');
            $list = $data['filters'];
            if ($index !== null && $index !== '' && isset($list[(int) $index])) {
                $list[(int) $index] = $filter;
            } else {
                $list[] = $filter;
            }
            $this->filters->save((string) $box['address'], $list, $data['responder'], 'filter saved');
            return 'Filter “' . $filter['name'] . '” saved.';
        });
    }

    public function toggle(Request $request, string $id, string $mailbox, string $n): Response
    {
        return $this->change($request, $id, $mailbox, function (array $domain, array $box, array $data) use ($n): string {
            $list = $data['filters'];
            if (!isset($list[(int) $n])) {
                throw new UserFacingException('The filter no longer exists.');
            }
            $list[(int) $n]['enabled'] = empty($list[(int) $n]['enabled']);
            $this->filters->save((string) $box['address'], $list, $data['responder'], 'filter toggled');
            return 'Filter “' . $list[(int) $n]['name'] . '” ' . ($list[(int) $n]['enabled'] ? 'enabled' : 'disabled') . '.';
        });
    }

    public function delete(Request $request, string $id, string $mailbox, string $n): Response
    {
        return $this->change($request, $id, $mailbox, function (array $domain, array $box, array $data) use ($n): string {
            $list = $data['filters'];
            if (!isset($list[(int) $n])) {
                throw new UserFacingException('The filter no longer exists.');
            }
            $name = (string) $list[(int) $n]['name'];
            array_splice($list, (int) $n, 1);
            $this->filters->save((string) $box['address'], $list, $data['responder'], 'filter deleted');
            return 'Filter “' . $name . '” deleted.';
        });
    }

    public function responder(Request $request, string $id, string $mailbox): Response
    {
        return $this->change($request, $id, $mailbox, function (array $domain, array $box, array $data) use ($request): string {
            $r = MailFilterService::responderFromForm($request->all());
            $this->filters->save((string) $box['address'], $data['filters'], $r, 'autoresponder');
            return $r['enabled'] ? 'Autoresponder on.' : 'Autoresponder off.';
        });
    }

    public function routing(Request $request, string $id): Response
    {
        $domain = $this->mail->domain((int) $id, $this->user($request));
        if ($domain === null) {
            return $this->redirectWithError('/mail', 'Mail domain not found.');
        }
        try {
            $this->filters->setRouting($domain, $request->string('routing'));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail/domains/' . (int) $domain['id'] . '#routing', $e);
        }
        return $this->redirectWithSuccess('/mail/domains/' . (int) $domain['id'] . '#routing', 'Mail routing: ' . MailFilterService::ROUTING[$request->string('routing')] . '.');
    }
}
