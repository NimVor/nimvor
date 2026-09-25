package shacrypt

import (
	"strings"
	"testing"
)

// Vectors from the SHA-crypt specification, openssl and glibc.
func TestSHA512Crypt(t *testing.T) {
	for _, v := range []struct {
		pw, salt string
		rounds   int
		want     string
	}{
		{"Hello world!", "saltstring", 5000, "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"Hello world!", "saltstringsaltstring", 10000, "$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
		{"we have a short salt string but not a short password", "roundstoolow", 5000, "$6$roundstoolow$nqr8LEsVXi32.wekO0SEtgy3Bh.LjQL3yyKo0pYzSTuALtgqAe77gHXN9uyaIj.D5SMTHDcXGT/yedSynsfqW1"}, // openssl passwd -6
		{"x", "abcdefgh", 100000, "$6$rounds=100000$abcdefgh$qbJpf2s4Ort4cnosPYoHHN7u4BCyLN.NvJSxKv8a9HYkHyva/7btWJAwSIwWu.tDfgTRO/Jg5C02MFhA8FB/S/"},                                            // glibc crypt
	} {
		if got := sha512Crypt(v.pw, v.salt, v.rounds); got != v.want {
			t.Errorf("sha512Crypt(%q, %q, %d) = %s", v.pw, v.salt, v.rounds, got)
		}
	}
	h := HashPassword("x")
	if !strings.HasPrefix(h, "$6$rounds=100000$") || h == HashPassword("x") {
		t.Fatalf("HashPassword = %s", h)
	}
}
