<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$status = [
    'https' => true, 'mode' => 'letsencrypt', 'force_https' => true, 'hsts' => false, 'security_headers' => false, 'state' => 'valid',
    'certificate' => ['common_name' => 'bluebird-cafe.example', 'names' => ['bluebird-cafe.example', 'www.bluebird-cafe.example'], 'issuer' => 'C=US, O=Let\'s Encrypt, CN=R11', 'not_before' => '2026-11-01T08:00:00Z', 'not_after' => '2027-01-30T08:00:00Z', 'days_remaining' => 67, 'self_signed' => false, 'fingerprint_sha256' => 'A1:B2:C3:D4'],
    'auto_renewal' => true,
];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$ref = O::ref('SslStatus');

return [
    'schemas' => [
        'SslStatus' => [
            'type' => 'object',
            'properties' => [
                'https' => ['type' => 'boolean', 'description' => 'Whether the website is served over HTTPS.'],
                'mode' => ['type' => 'string', 'enum' => ['none', 'letsencrypt', 'custom'], 'description' => '`letsencrypt` renews automatically; `custom` is an uploaded certificate that you renew yourself.'],
                'force_https' => ['type' => 'boolean', 'description' => 'HTTP requests are redirected to HTTPS.'],
                'hsts' => ['type' => 'boolean', 'description' => 'The HSTS header is sent (only with `force_https`).'],
                'security_headers' => ['type' => 'boolean', 'description' => 'The standard security headers are added.'],
                'state' => ['type' => ['string', 'null'], 'enum' => ['valid', 'expiring', 'expired', 'error', null], 'description' => '`expiring` means the certificate is within the server\'s warning period (14 days by default). Null without a certificate.'],
                'certificate' => ['type' => ['object', 'null'], 'description' => 'The installed certificate, read from the server. Null without HTTPS.', 'properties' => [
                    'common_name' => ['type' => 'string'],
                    'names' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Every name the certificate covers.'],
                    'issuer' => ['type' => 'string'],
                    'not_before' => ['type' => ['string', 'null'], 'format' => 'date-time'],
                    'not_after' => ['type' => ['string', 'null'], 'format' => 'date-time'],
                    'days_remaining' => ['type' => 'integer'],
                    'self_signed' => ['type' => 'boolean'],
                    'fingerprint_sha256' => ['type' => 'string'],
                ]],
                'auto_renewal' => ['type' => ['boolean', 'null'], 'description' => 'Whether the server\'s renewal timer is active. Null for an uploaded certificate.'],
            ],
        ],
    ],
    'paths' => [
        '/websites/{id}/ssl' => [
            'get' => [
                'operationId' => 'getWebsiteSsl',
                'tags' => ['SSL'],
                'summary' => 'Get a website\'s HTTPS state',
                'description' => 'The settings and the installed certificate (read from the server). Use `POST /websites/{id}/ssl` to issue a free Let\'s Encrypt certificate.',
                'x-permission' => 'ssl.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The HTTPS state.', $ref, $status), '404' => O::err('NotFound')],
            ],
            'delete' => [
                'operationId' => 'disableWebsiteSsl',
                'tags' => ['SSL'],
                'summary' => 'Turn HTTPS off',
                'description' => 'The website is served over plain HTTP again and the redirect and HSTS are removed. Add `?delete_certificate=1` to also delete a Let\'s Encrypt certificate from the server (a later `POST` issues a new one).',
                'x-permission' => 'ssl.manage',
                'parameters' => [$id, ['name' => 'delete_certificate', 'in' => 'query', 'description' => 'Pass `1` to delete the Let\'s Encrypt certificate too.', 'schema' => ['type' => 'string', 'enum' => ['1']]]],
                'responses' => ['200' => O::res('The HTTPS state, now off.', $ref, ['https' => false, 'mode' => 'none', 'force_https' => false, 'hsts' => false, 'state' => null, 'certificate' => null, 'auto_renewal' => null] + $status), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/ssl/custom' => ['put' => [
            'operationId' => 'installWebsiteCertificate',
            'tags' => ['SSL'],
            'summary' => 'Install your own certificate',
            'description' => 'Uploads a certificate and its private key (PEM). The pair is checked on the server (the key must match, and the certificate must be valid for the website) before HTTPS is switched on and the web server is reloaded; if the check fails nothing changes. The private key is never returned or logged. An uploaded certificate is not renewed automatically.',
            'x-permission' => 'ssl.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['certificate', 'private_key'], 'properties' => [
                'certificate' => ['type' => 'string', 'description' => 'The certificate, PEM encoded (`-----BEGIN CERTIFICATE-----`…).'],
                'private_key' => ['type' => 'string', 'description' => 'The matching private key, PEM encoded.'],
                'chain' => ['type' => 'string', 'description' => 'The intermediate certificates, PEM encoded (optional).'],
            ]], ['certificate' => "-----BEGIN CERTIFICATE-----\nMIID…\n-----END CERTIFICATE-----", 'private_key' => "-----BEGIN PRIVATE KEY-----\nMIIE…\n-----END PRIVATE KEY-----"]),
            'responses' => ['200' => O::res('The HTTPS state with the new certificate.', $ref, ['mode' => 'custom', 'auto_renewal' => null] + $status), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/ssl/renew' => ['post' => [
            'operationId' => 'renewWebsiteSsl',
            'tags' => ['SSL'],
            'summary' => 'Renew the Let\'s Encrypt certificate now',
            'description' => 'Renewal normally happens by itself; this forces it and reloads the web server. It can take up to a few minutes. Only for Let\'s Encrypt certificates (`409` for an uploaded one).',
            'x-permission' => 'ssl.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The HTTPS state with the renewed certificate.', $ref, $status), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/ssl/options' => ['patch' => [
            'operationId' => 'updateWebsiteSslOptions',
            'tags' => ['SSL'],
            'summary' => 'Change the HTTPS options',
            'description' => 'Only the fields you send change. `force_https` and `hsts` need HTTPS to be on (`409` otherwise); HSTS only takes effect together with `force_https`, and turning the redirect off turns HSTS off. The web server configuration is checked before it is reloaded.',
            'x-permission' => 'ssl.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'properties' => [
                'force_https' => ['type' => 'boolean', 'description' => 'Redirect HTTP to HTTPS.'],
                'hsts' => ['type' => 'boolean', 'description' => 'Send the Strict-Transport-Security header.'],
                'security_headers' => ['type' => 'boolean', 'description' => 'Add the standard security headers.'],
            ]], ['force_https' => true, 'hsts' => true]),
            'responses' => ['200' => O::res('The HTTPS state, changed.', $ref, ['hsts' => true] + $status), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
    ],
];
