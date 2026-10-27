<?php

declare(strict_types=1);

namespace NimVor\Support;

/** Public DNS answers, through the system resolver (replaceable in tests). */
class DnsLookup
{
    /** @return list<string> IPv4 addresses of the name */
    public function a(string $name): array
    {
        $r = @dns_get_record($name, DNS_A);
        return is_array($r) ? array_values(array_filter(array_map(static fn (array $x): string => (string) ($x['ip'] ?? ''), $r))) : [];
    }

    /** @return list<string> name servers of the zone, lower case, without the final dot */
    public function ns(string $name): array
    {
        $r = @dns_get_record($name, DNS_NS);
        return is_array($r) ? array_values(array_filter(array_map(static fn (array $x): string => rtrim(strtolower((string) ($x['target'] ?? '')), '.'), $r))) : [];
    }
}
