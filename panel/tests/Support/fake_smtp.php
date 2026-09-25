<?php

// Tiny SMTP server for tests: one session, records the transcript.
// Usage: php fake_smtp.php <transcript file> <mode: plain|starttls|nostarttls>
declare(strict_types=1);

[$_, $file, $mode] = $argv;
$ctx = stream_context_create();
if ($mode === 'starttls') {
    $key = openssl_pkey_new(['private_key_bits' => 2048]);
    $cert = openssl_csr_sign(openssl_csr_new(['commonName' => 'untrusted.test'], $key), null, $key, 1);
    openssl_x509_export($cert, $pem);
    openssl_pkey_export($key, $keyPem);
    $pemFile = tempnam(sys_get_temp_dir(), 'smtp');
    file_put_contents($pemFile, $pem . $keyPem);
    stream_context_set_option($ctx, 'ssl', 'local_cert', $pemFile);
}
$server = stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr, STREAM_SERVER_BIND | STREAM_SERVER_LISTEN, $ctx);
echo parse_url('tcp://' . stream_socket_get_name($server, false), PHP_URL_PORT), "\n";
fflush(STDOUT);
$c = @stream_socket_accept($server, 20);
if ($c === false) {
    exit(1);
}
$log = '';
$say = function (string $l) use ($c, &$log): void {
    fwrite($c, $l . "\r\n");
    $log .= 'S: ' . $l . "\n";
};
$say('220 fake ESMTP');
$data = false;
while (($line = fgets($c)) !== false) {
    $log .= 'C: ' . rtrim($line, "\r\n") . "\n";
    if ($data) {
        if (rtrim($line, "\r\n") === '.') {
            $data = false;
            $say('250 queued');
        }
        continue;
    }
    $cmd = strtoupper(substr(trim($line), 0, 4));
    if ($cmd === 'EHLO') {
        fwrite($c, "250-fake\r\n" . ($mode === 'starttls' ? "250-STARTTLS\r\n" : '') . "250 AUTH PLAIN LOGIN\r\n");
    } elseif ($cmd === 'STAR') {
        $say('220 go ahead');
        if (@stream_socket_enable_crypto($c, true, STREAM_CRYPTO_METHOD_TLS_SERVER) !== true) {
            break;
        }
    } elseif ($cmd === 'AUTH') {
        $say(str_contains($line, base64_encode("\0mailer\0s3cret")) ? '235 ok' : '535 bad credentials');
    } elseif ($cmd === 'DATA') {
        $data = true;
        $say('354 end with .');
    } elseif ($cmd === 'QUIT') {
        $say('221 bye');
        break;
    } else {
        $say('250 ok');
    }
}
file_put_contents($file, $log);
