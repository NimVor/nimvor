<?php

declare(strict_types=1);

namespace NimVor\Security;

use NimVor\Http\Request;
use NimVor\Services\SettingsService;

/**
 * The origin and relying-party ID security keys are bound to: the public
 * panel URL when it is set, otherwise the address being browsed.
 */
final class WebAuthnOrigin
{
    /** @return array{origin: string, rp_id: string} */
    public static function of(Request $request, SettingsService $settings): array
    {
        $url = $settings->string('panel_url');
        if ($url !== '' && ($host = parse_url($url, PHP_URL_HOST)) !== null && $host !== false) {
            $port = parse_url($url, PHP_URL_PORT);
            $scheme = (string) parse_url($url, PHP_URL_SCHEME);
            return ['origin' => $scheme . '://' . $host . ($port ? ':' . $port : ''), 'rp_id' => strtolower($host)];
        }
        $hostHeader = strtolower($request->header('host'));
        $host = (string) parse_url('http://' . $hostHeader, PHP_URL_HOST);
        return ['origin' => ($request->isSecure() ? 'https' : 'http') . '://' . $hostHeader, 'rp_id' => $host];
    }
}
