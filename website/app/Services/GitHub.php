<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/**
 * What the site reads from GitHub: the repository's Markdown files, its issues and
 * its sponsors. Only api.github.com and raw.githubusercontent.com are contacted.
 * A token is optional (a higher rate limit) except for sponsors, which GitHub only
 * hands to a signed-in maintainer.
 */
final class GitHub
{
    /** @param array{repo: string, branch: string, token: string} $cfg */
    public function __construct(private readonly Http $http, private readonly array $cfg)
    {
    }

    public function repoUrl(): string
    {
        return 'https://github.com/' . $this->cfg['repo'];
    }

    public function hasToken(): bool
    {
        return $this->cfg['token'] !== '';
    }

    /** @return list<array{path: string, sha: string}> every Markdown file in the repository */
    public function markdownFiles(): array
    {
        $data = $this->json('GET', 'https://api.github.com/repos/' . $this->cfg['repo'] . '/git/trees/' . rawurlencode($this->cfg['branch']) . '?recursive=1');
        $out = [];
        foreach ((array) ($data['tree'] ?? []) as $e) {
            if (($e['type'] ?? '') === 'blob' && is_string($e['path'] ?? null) && str_ends_with($e['path'], '.md') && is_string($e['sha'] ?? null)) {
                $out[] = ['path' => $e['path'], 'sha' => $e['sha']];
            }
        }
        if ($out === []) {
            throw new GitHubException('GitHub listed no documentation files.');
        }
        return $out;
    }

    public function raw(string $path): string
    {
        $url = 'https://raw.githubusercontent.com/' . $this->cfg['repo'] . '/' . rawurlencode($this->cfg['branch']) . '/' . implode('/', array_map('rawurlencode', explode('/', $path)));
        $r = $this->call('GET', $url, ['Accept' => 'text/plain']);
        if ($r['status'] !== 200) {
            throw new GitHubException('GitHub did not return ' . $path . ' (HTTP ' . $r['status'] . ').');
        }
        return $r['body'];
    }

    /**
     * @return list<array{number: int, title: string, url: string, state: string, reason: string, labels: list<string>, assignees: int, comments: int, author: string, created_at: string, updated_at: string, closed_at: ?string}>
     */
    public function issues(int $pages = 2): array
    {
        $out = [];
        for ($p = 1; $p <= $pages; $p++) {
            $rows = $this->json('GET', 'https://api.github.com/repos/' . $this->cfg['repo'] . '/issues?state=all&sort=updated&direction=desc&per_page=100&page=' . $p);
            foreach ($rows as $i) {
                if (!is_array($i) || isset($i['pull_request']) || !is_int($i['number'] ?? null)) {
                    continue;
                }
                $out[] = [
                    'number' => $i['number'],
                    'title' => mb_substr((string) ($i['title'] ?? ''), 0, 200),
                    'url' => 'https://github.com/' . $this->cfg['repo'] . '/issues/' . $i['number'],
                    'state' => ($i['state'] ?? 'open') === 'closed' ? 'closed' : 'open',
                    'reason' => (string) ($i['state_reason'] ?? ''),
                    'labels' => array_values(array_map(static fn ($l): string => mb_substr((string) (is_array($l) ? ($l['name'] ?? '') : $l), 0, 40), (array) ($i['labels'] ?? []))),
                    'assignees' => count((array) ($i['assignees'] ?? [])),
                    'comments' => (int) ($i['comments'] ?? 0),
                    'author' => (string) ($i['user']['login'] ?? ''),
                    'created_at' => (string) ($i['created_at'] ?? ''),
                    'updated_at' => (string) ($i['updated_at'] ?? ''),
                    'closed_at' => isset($i['closed_at']) ? (string) $i['closed_at'] : null,
                ];
            }
            if (count($rows) < 100) {
                break;
            }
        }
        return $out;
    }

    /**
     * The public, active sponsors of an organisation or user (needs a token that can read them).
     *
     * @return list<array{login: string, name: string, url: string, monthly_usd: int, one_time: bool}>
     */
    public function sponsors(string $login): array
    {
        if (!$this->hasToken()) {
            throw new GitHubException('A GitHub token is needed to read sponsors (set GITHUB_TOKEN).');
        }
        $fields = 'sponsorshipsAsMaintainer(first: 100, activeOnly: true) { nodes { privacyLevel isOneTimePayment tier { monthlyPriceInDollars } sponsorEntity { __typename ... on User { login name url } ... on Organization { login name url } } } }';
        $nodes = null;
        foreach (['organization', 'user'] as $kind) {
            $res = $this->json('POST', 'https://api.github.com/graphql', ['query' => 'query($l: String!) { ' . $kind . '(login: $l) { ' . $fields . ' } }', 'variables' => ['l' => $login]], true);
            $found = $res['data'][$kind]['sponsorshipsAsMaintainer']['nodes'] ?? null;
            if (is_array($found)) {
                $nodes = $found;
                break;
            }
        }
        if ($nodes === null) {
            throw new GitHubException('GitHub did not return sponsors for ' . $login . ' (check the token\'s access).');
        }
        $out = [];
        foreach ($nodes as $n) {
            $e = $n['sponsorEntity'] ?? null;
            if (!is_array($e) || ($n['privacyLevel'] ?? '') !== 'PUBLIC' || !is_string($e['login'] ?? null)) {
                continue;
            }
            $out[] = [
                'login' => $e['login'],
                'name' => mb_substr((string) (($e['name'] ?? '') !== '' ? $e['name'] : $e['login']), 0, 120),
                'url' => 'https://github.com/' . rawurlencode($e['login']),
                'monthly_usd' => (int) ($n['tier']['monthlyPriceInDollars'] ?? 0),
                'one_time' => (bool) ($n['isOneTimePayment'] ?? false),
            ];
        }
        return $out;
    }

    /**
     * @param array<string, mixed>|null $body
     * @return array<string|int, mixed>
     */
    private function json(string $method, string $url, ?array $body = null, bool $auth = false): array
    {
        $r = $this->call($method, $url, ['Accept' => 'application/vnd.github+json'] + ($body !== null ? ['Content-Type' => 'application/json'] : []), $body === null ? null : json_encode($body, JSON_UNESCAPED_SLASHES), $auth);
        if ($r['status'] !== 200) {
            throw new GitHubException($this->why($r['status']));
        }
        $data = json_decode($r['body'], true);
        if (!is_array($data)) {
            throw new GitHubException('GitHub answered with something unexpected.');
        }
        return $data;
    }

    /**
     * @param array<string, string> $headers
     * @return array{status: int, body: string}
     */
    private function call(string $method, string $url, array $headers, ?string $body = null, bool $auth = false): array
    {
        $host = (string) parse_url($url, PHP_URL_HOST);
        if (!in_array($host, ['api.github.com', 'raw.githubusercontent.com'], true)) {
            throw new GitHubException('Refusing to contact ' . $host . '.');
        }
        if ($this->cfg['token'] !== '' && ($auth || $host === 'api.github.com')) {
            $headers['Authorization'] = 'Bearer ' . $this->cfg['token'];
        }
        try {
            return $this->http->request($method, $url, $headers, $body, 15);
        } catch (\RuntimeException $e) {
            throw new GitHubException('GitHub could not be reached: ' . $e->getMessage(), 0, $e);
        }
    }

    private function why(int $status): string
    {
        return match (true) {
            $status === 401 => 'GitHub did not accept the token.',
            $status === 403 || $status === 429 => 'GitHub refused the request: it is limiting requests right now, or the token lacks access (set GITHUB_TOKEN for a higher limit).',
            $status === 404 => 'GitHub could not find the repository.',
            default => 'GitHub answered HTTP ' . $status . '.',
        };
    }
}
