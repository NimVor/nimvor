package migrate

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// maxZoneBytes is the most of a zone file that is read from a source.
const maxZoneBytes = 1 << 20

// ReadZone returns the zone file of a domain from a cPanel source (/var/named/<domain>.db). It only
// reads: nothing is changed on the source, and nothing here touches DNS anywhere.
func ReadZone(ctx context.Context, t Transport, panel, domain string) (string, error) {
	if panel != "cpanel" {
		return "", fmt.Errorf("zone files are read only from cPanel sources in this version")
	}
	if !domainName.MatchString(domain) {
		return "", fmt.Errorf("%q is not a domain name", domain)
	}
	var out bytes.Buffer
	cmd := fmt.Sprintf("sh -c %s", Shq("f=/var/named/"+domain+".db; [ -f \"$f\" ] && [ ! -L \"$f\" ] && head -c "+fmt.Sprint(maxZoneBytes+1)+" \"$f\""))
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
