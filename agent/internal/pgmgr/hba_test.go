package pgmgr

import (
	"strings"
	"testing"
)

// The RHEL family's pg_hba.conf, as the distribution ships it.
const rhelHBA = `# TYPE  DATABASE        USER            ADDRESS                 METHOD
local   all             all                                     peer
host    all             all             127.0.0.1/32            ident
host    all             all             ::1/128                 ident
`

func TestHBABlockGoesFirstAndLeavesTheRestAlone(t *testing.T) {
	block := hbaBlock(16)
	got, changed := withHBABlock(rhelHBA, block)
	if !changed {
		t.Fatal("a file without the block is changed")
	}
	if !strings.HasPrefix(got, hbaBegin) {
		t.Fatalf("the block is not first:\n%s", got)
	}
	// Rules are first-match: the password rule must come before the ident line it replaces.
	if strings.Index(got, "scram-sha-256") > strings.Index(got, "ident") {
		t.Fatalf("the ident rule would win:\n%s", got)
	}
	if !strings.HasSuffix(got, rhelHBA) {
		t.Fatalf("the original lines were changed:\n%s", got)
	}
}

func TestHBABlockIsIdempotentAndRemovable(t *testing.T) {
	block := hbaBlock(16)
	once, _ := withHBABlock(rhelHBA, block)
	twice, changed := withHBABlock(once, block)
	if changed || twice != once {
		t.Fatal("adding the block twice must change nothing")
	}
	if withoutHBABlock(once) != rhelHBA {
		t.Fatal("removing the block must give back the original file")
	}
	// A different method (a newer server) replaces the block instead of stacking a second one.
	re, changed := withHBABlock(once, hbaBlock(12))
	if !changed || strings.Count(re, hbaBegin) != 1 || !strings.Contains(re, "md5") {
		t.Fatalf("the block was not replaced:\n%s", re)
	}
}

func TestHBAMethodFollowsTheServerVersion(t *testing.T) {
	// "md5" also accepts a SCRAM-stored password; scram-sha-256 would refuse an md5-stored one (the default before 14).
	if !strings.Contains(hbaBlock(13), " md5") || strings.Contains(hbaBlock(13), "scram") {
		t.Fatal("PostgreSQL 13 needs md5")
	}
	if !strings.Contains(hbaBlock(14), "scram-sha-256") || !strings.Contains(hbaBlock(16), "scram-sha-256") {
		t.Fatal("PostgreSQL 14 and newer use scram-sha-256")
	}
}

func TestAnUnfinishedBlockIsLeftForAPerson(t *testing.T) {
	broken := hbaBegin + "\nhost all all 127.0.0.1/32 md5\n" + rhelHBA
	if withoutHBABlock(broken) != broken {
		t.Fatal("a block without an end marker must not be cut")
	}
}
