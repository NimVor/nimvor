package migrate

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// maxZoneBytes is the most of a zone file that is read from a source.
const maxZoneBytes = 1 << 20

// zoneFinders say where each panel keeps a domain's BIND zone file. $d is the domain (already checked).
// The first existing regular file is used.
var zoneFinders = map[string]string{
	"cpanel":      `/var/named/$d.db`,
	"directadmin": `/var/named/$d.db`,
	"vestacp":     `/home/*/conf/dns/$d.db`,
	"hestiacp":    `/home/*/conf/dns/$d.db`,
	"ispconfig":   `/etc/bind/pri.$d /var/lib/bind/pri.$d`,
}

// HasZoneFiles says whether zone files can be read from this kind of source.
func HasZoneFiles(panel string) bool { _, ok := zoneFinders[panel]; return ok }

// ReadZone returns the BIND zone file of a domain from a source (see zoneFinders). It only reads: nothing
// is changed on the source, and nothing here touches DNS anywhere.
func ReadZone(ctx context.Context, t Transport, panel, domain string) (string, error) {
	paths, ok := zoneFinders[panel]
	if !ok {
		return "", fmt.Errorf("zone files are not read from this kind of source in this version")
	}
	if !domainName.MatchString(domain) {
		return "", fmt.Errorf("%q is not a domain name", domain)
	}
	var out bytes.Buffer
	script := "d=" + Shq(domain) + "; for f in " + paths + "; do [ -f \"$f\" ] && [ ! -L \"$f\" ] && { head -c " + fmt.Sprint(maxZoneBytes+1) + " \"$f\"; break; }; done"
	cmd := fmt.Sprintf("sh -c %s", Shq(script))
	if _, err := t.Exec(ctx, cmd, &out); err != nil {
		return "", fmt.Errorf("the zone file of %s could not be read (is it hosted on this server?): %v", domain, err)
	}
	if out.Len() == 0 {
		return "", fmt.Errorf("the zone file of %s was not found on the source", domain)
	}
	if out.Len() > maxZoneBytes {
		return "", fmt.Errorf("the zone file of %s is larger than 1 MB", domain)
	}
	return strings.ReplaceAll(out.String(), "\r", ""), nil
}
