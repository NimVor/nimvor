package nginx

import "testing"

func TestParseBasic(t *testing.T) {
	src := `
# comment
user www-data;
http {
    server {
        listen 443 ssl; # trailing comment
        server_name "example.com" 'www.example.com';
        location ~ \.php$ { fastcgi_pass unix:/run/php/x.sock; }
        rewrite ^/(.*)$ /index.php?q=${1} last;
    }
}`
	dirs, err := Parse(src, "test.conf")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 || dirs[0].Name != "user" || dirs[1].Name != "http" {
		t.Fatalf("unexpected top level: %+v", dirs)
	}
	srv := dirs[1].Block[0]
	if srv.Name != "server" || !srv.HasBlock || len(srv.Block) != 4 {
		t.Fatalf("unexpected server block: %+v", srv)
	}
	names := srv.Block[1].Args
	if len(names) != 2 || names[0] != "example.com" || names[1] != "www.example.com" {
		t.Fatalf("server_name args = %v", names)
	}
	if srv.Block[2].Args[1] != `\.php$` {
		t.Fatalf("location regex = %q", srv.Block[2].Args[1])
	}
	if srv.Block[3].Args[1] != "/index.php?q=${1}" {
		t.Fatalf("variable with braces split: %q", srv.Block[3].Args[1])
	}
	if srv.Line != 5 {
		t.Fatalf("server line = %d, want 5", srv.Line)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"server { listen 80;",
		"listen 80",
		"}",
		`server_name "unterminated;`,
		"{ }",
	}
	for _, src := range bad {
		if _, err := Parse(src, "bad.conf"); err == nil {
			t.Errorf("Parse(%q) expected error", src)
		}
	}
}

func TestParseDeepNestingRejected(t *testing.T) {
	src := ""
	for i := 0; i < 60; i++ {
		src += "a {"
	}
	for i := 0; i < 60; i++ {
		src += "}"
	}
	if _, err := Parse(src, "deep.conf"); err == nil {
		t.Fatal("expected nesting error")
	}
}
