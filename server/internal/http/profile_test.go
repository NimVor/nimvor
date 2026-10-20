package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func TestProfileShowsOwnAccountAndTokens(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (2,'customer','Customer','own','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'bob','bob@example.test','Bob Jones',?,2,1,'2026-01-01T00:00:00Z','',''),
		(2,'amy','amy@example.test','Amy',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash)
	// bob's token and amy's token (amy's must never appear on bob's profile).
	exec(`INSERT INTO api_tokens (id,user_id,name,token_hash,prefix,ability,created_at) VALUES
		(1,1,'CI deploy','h1','whcp_bob','read','2026-01-01'),
		(2,2,'Amy token','h2','whcp_amy','read','2026-01-01')`)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	c, tok := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {tok}, "username": {"bob"}, "password": {loginPassword}}, c)
	cookie := rr.Result().Cookies()[0]

	body := get(t, h, "/profile", cookie).Body.String()
	if !strings.Contains(body, "Bob Jones") || !strings.Contains(body, "bob@example.test") {
		t.Fatalf("profile missing own details:\n%s", body)
	}
	if !strings.Contains(body, "CI deploy") || !strings.Contains(body, "whcp_bob") {
		t.Fatalf("profile missing own token:\n%s", body)
	}
	// The secret itself must never render — only the prefix.
	if strings.Contains(body, "h1") && strings.Contains(body, "token_hash") {
		t.Fatalf("profile leaked token hash")
	}
	// Another user's token must not appear.
	if strings.Contains(body, "Amy token") || strings.Contains(body, "whcp_amy") {
		t.Fatalf("TOKEN LEAK: bob's profile showed another user's token:\n%s", body)
	}
}

func TestProfileRequiresAuth(t *testing.T) {
	db, _ := database.Open(":memory:")
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	if rr := get(t, h, "/profile", nil); rr.Code == http.StatusOK {
		t.Fatal("/profile served without authentication")
	}
}
