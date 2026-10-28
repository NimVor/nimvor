<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * A website's redirects, added and removed one by one. They are stored with
 * the website (websites.redirects) and rendered by the agent as exact-path
 * redirects in the website's Nginx configuration; a change is applied with
 * the website and undone if the agent refuses it.
 */
final class RedirectsService
{
    public const MAX = 100;

    /** Redirect types: status code => [label, help]. */
    public const TYPES = [
        301 => ['Permanent (301)', 'The move is final: browsers and search engines remember the new address.'],
        302 => ['Temporary (302)', 'The move is for now: the old address stays the one that counts.'],
    ];

    public function __construct(
        private readonly WebsiteService $websites,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return list<array{from: string, to: string, code: int}>
     */
    public function list(array $site): array
    {
        $out = [];
        foreach ((array) ($site['redirects'] ?? []) as $r) {
            $out[] = ['from' => (string) ($r['from'] ?? ''), 'to' => (string) ($r['to'] ?? ''), 'code' => (int) ($r['code'] ?? 301)];
        }
        return $out;
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $input
     */
    public function add(array $site, array $input): string
    {
        $errors = [];
        $code = filter_var($input['code'] ?? 301, FILTER_VALIDATE_INT);
        if ($code === false || !isset(self::TYPES[$code])) {
            $errors['code'] = 'Choose permanent (301) or temporary (302).';
            $code = 301;
        }
        $from = trim((string) ($input['from'] ?? ''));
        if ($from !== '' && $from[0] !== '/') {
            $from = '/' . $from;
        }
        if ($from === '' || !preg_match('#^/[A-Za-z0-9._~/-]{0,200}$#', $from) || str_contains($from, '//') || str_contains($from, '..')) {
            $errors['from'] = 'Enter the path to redirect, such as /old-page (letters, digits and . _ ~ - / only).';
        }
        $to = trim((string) ($input['to'] ?? ''));
        if ($to === '' || strlen($to) > 400 || !preg_match('#^(?:https?://[A-Za-z0-9.-]+(?::[0-9]{1,5})?)?(?:/[A-Za-z0-9._~/%?=&+-]{0,300})?$#', $to)) {
            $errors['to'] = 'Enter where it should go: a full address such as https://example.com/new-page, or a path such as /new-page.';
        }
        $existing = $this->list($site);
        if (!isset($errors['from'])) {
            foreach ($existing as $r) {
                if ($r['from'] === $from) {
                    $errors['from'] = $from . ' already has a redirect. Delete it first to replace it.';
                }
            }
        }
        if (!isset($errors['from']) && !isset($errors['to'])) {
            // A redirect to the page itself loops forever.
            $self = $to === $from || preg_match('#^https?://' . preg_quote((string) $site['domain'], '#') . '(?::[0-9]+)?' . preg_quote($from, '#') . '/?$#i', $to) === 1;
            if ($self) {
                $errors['to'] = 'This redirects the page to itself. Choose a different destination.';
            }
        }
        if (count($existing) >= self::MAX && $errors === []) {
            $errors['from'] = 'A website can have at most ' . self::MAX . ' redirects.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $existing[] = ['from' => $from, 'to' => $to, 'code' => (int) $code];
        $this->websites->patch($site, ['redirects' => $existing], 'website.redirect_add');
        return $from . ' now redirects to ' . $to . '.';
    }

    /** @param array<string, mixed> $site */
    public function remove(array $site, string $from): string
    {
        $existing = $this->list($site);
        $left = array_values(array_filter($existing, static fn (array $r): bool => $r['from'] !== $from));
        if (count($left) === count($existing)) {
            throw new UserFacingException('That redirect does not exist (it may already be deleted).');
        }
        $this->websites->patch($site, ['redirects' => $left], 'website.redirect_remove');
        return 'The redirect from ' . $from . ' was deleted.';
    }
}
