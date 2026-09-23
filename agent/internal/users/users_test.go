package users

import "testing"

func TestParse(t *testing.T) {
	us := Parse([]byte("root:x:0:0:root:/root:/bin/bash\ndeploy:x:1000:1000::/home/deploy:/bin/bash\nsvc:x:998:998::/nonexistent:/usr/sbin/nologin\nbad:line\n"))
	if len(us) != 3 {
		t.Fatalf("users = %+v", us)
	}
	if !us[0].System || !us[0].CanLogin || us[1].System || us[2].CanLogin {
		t.Errorf("flags wrong: %+v", us)
	}
}
