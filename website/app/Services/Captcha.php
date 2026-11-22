<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Cache\Cache;
use NimVorSite\Core\Request;
use NimVorSite\Repositories\State;

/**
 * The check in front of the public forms (sign in, register, contact, build services, a visitor's ticket reply).
 *
 * Providers:
 *  - builtin   (default) a small question the site asks and checks itself. Nothing is sent to a third party,
 *              no cookie is set and it works without JavaScript. The answer is bound to a signed, single-use,
 *              expiring token; answering faster than a person could is refused.
 *  - turnstile Cloudflare Turnstile (CAPTCHA_SITE_KEY / CAPTCHA_SECRET_KEY).
 *  - hcaptcha  hCaptcha (the same two keys).
 *  - off       nothing is asked (tests, or a site that sits behind its own protection).
 * The built-in question stops simple scripts; a determined attacker can solve it, so a site that is attacked
 * should switch to Turnstile or hCaptcha (the Content-Security-Policy is widened to that provider only then).
 */
final class Captcha
{
    public const PROVIDERS = ['builtin', 'turnstile', 'hcaptcha', 'off'];
    private const DEFAULT_MIN_AGE = 3;
    private const MAX_AGE = 1800;
    private const WORDS = [1 => 'one', 2 => 'two', 3 => 'three', 4 => 'four', 5 => 'five', 6 => 'six', 7 => 'seven', 8 => 'eight', 9 => 'nine', 10 => 'ten'];

    private string $provider;

    /** @param array{provider?: string, site_key?: string, secret_key?: string, min_age?: int} $config */
    public function __construct(private readonly State $state, private readonly Cache $cache, private readonly Http $http, private readonly array $config)
    {
        $p = (string) ($config['provider'] ?? 'builtin');
        $this->provider = in_array($p, self::PROVIDERS, true) ? $p : 'builtin';
        // A provider that needs keys but has none cannot work: fall back to the built-in question, never to "off".
        if (in_array($this->provider, ['turnstile', 'hcaptcha'], true) && (($config['site_key'] ?? '') === '' || ($config['secret_key'] ?? '') === '')) {
            $this->provider = 'builtin';
        }
    }

    public function provider(): string
    {
        return $this->provider;
    }

    public function enabled(): bool
    {
        return $this->provider !== 'off';
    }

    public function siteKey(): string
    {
        return (string) ($this->config['site_key'] ?? '');
    }

    /** The name of the form field that carries the answer. */
    public function field(): string
    {
        return match ($this->provider) {
            'turnstile' => 'cf-turnstile-response',
            'hcaptcha' => 'h-captcha-response',
            default => 'captcha_answer',
        };
    }

    /** Where the provider's script and frame come from, for the Content-Security-Policy and the form. */
    public function script(): string
    {
        return match ($this->provider) {
            'turnstile' => 'https://challenges.cloudflare.com/turnstile/v0/api.js',
            'hcaptcha' => 'https://js.hcaptcha.com/1/api.js',
            default => '',
        };
    }

    /** @return list<string> hosts to allow in script-src and frame-src */
    public function cspHosts(): array
    {
        return match ($this->provider) {
            'turnstile' => ['https://challenges.cloudflare.com'],
            'hcaptcha' => ['https://js.hcaptcha.com', 'https://hcaptcha.com', 'https://*.hcaptcha.com'],
            default => [],
        };
    }

    /** @return array{question: string, token: string} */
    public function challenge(): array
    {
        $a = random_int(1, 9);
        $b = random_int(1, 9);
        [$text, $answer] = match (random_int(0, 2)) {
            0 => [self::WORDS[$a] . ' plus ' . self::WORDS[$b], $a + $b],
            1 => [self::WORDS[max($a, $b)] . ' minus ' . self::WORDS[min($a, $b)], max($a, $b) - min($a, $b)],
            default => [self::WORDS[$a] . ' plus ' . self::WORDS[$b] . ' plus ' . self::WORDS[1], $a + $b + 1],
        };
        $nonce = bin2hex(random_bytes(8));
        $body = rtrim(strtr(base64_encode((string) json_encode(['t' => time(), 'n' => $nonce, 'a' => $this->mac($nonce . ':' . $answer)])), '+/', '-_'), '=');
        return ['question' => 'What is ' . $text . '? Answer with digits.', 'token' => $body . '.' . $this->mac($body)];
    }

    /** Whether the submitted form passed the check. Always true when the check is off. */
    public function verify(Request $req, string $ip): bool
    {
        return match ($this->provider) {
            'off' => true,
            'turnstile', 'hcaptcha' => $this->verifyRemote($req->input($this->field()), $ip),
            default => $this->verifyBuiltin($req->input('captcha_token'), trim($req->input('captcha_answer'))),
        };
    }

    private function verifyBuiltin(string $token, string $answer): bool
    {
        $parts = explode('.', $token);
        if (count($parts) !== 2 || $answer === '' || !preg_match('/^\d{1,3}$/', $answer) || !hash_equals($this->mac($parts[0]), $parts[1])) {
            return false;
        }
        $data = json_decode((string) base64_decode(strtr($parts[0], '-_', '+/'), true), true);
        if (!is_array($data) || !isset($data['t'], $data['n'], $data['a']) || !is_int($data['t']) || !is_string($data['n']) || !is_string($data['a'])) {
            return false;
        }
        $age = time() - $data['t'];
        if ($age < $this->minAge() || $age > self::MAX_AGE) {
            return false;
        }
        // One try per question, right or wrong: a form that fails is shown again with a new question.
        if ($this->cache->increment('captcha:used:' . $data['n'], self::MAX_AGE + 60) > 1) {
            return false;
        }
        return hash_equals($data['a'], $this->mac($data['n'] . ':' . (int) $answer));
    }

    private function verifyRemote(string $response, string $ip): bool
    {
        if ($response === '' || strlen($response) > 4096) {
            return false;
        }
        $url = $this->provider === 'turnstile' ? 'https://challenges.cloudflare.com/turnstile/v0/siteverify' : 'https://api.hcaptcha.com/siteverify';
        try {
            $r = $this->http->request('POST', $url, ['Content-Type' => 'application/x-www-form-urlencoded'], http_build_query(['secret' => (string) ($this->config['secret_key'] ?? ''), 'response' => $response, 'remoteip' => $ip]), 8);
        } catch (\RuntimeException) {
            error_log('nimvor-site captcha: the ' . $this->provider . ' service could not be reached');
            return false; // fail closed: a form that cannot be checked is not accepted
        }
        $json = json_decode($r['body'], true);
        return $r['status'] === 200 && is_array($json) && ($json['success'] ?? false) === true;
    }

    private function minAge(): int
    {
        return max(0, (int) ($this->config['min_age'] ?? self::DEFAULT_MIN_AGE));
    }

    private function mac(string $data): string
    {
        return hash_hmac('sha256', $data, $this->secret());
    }

    private function secret(): string
    {
        $s = $this->state->get('captcha:secret');
        if ($s === null || strlen($s) < 32) {
            $s = bin2hex(random_bytes(32));
            $this->state->set('captcha:secret', $s);
        }
        return $s;
    }
}
