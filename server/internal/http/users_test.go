package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func usersEnv(t *testing.T, loginAs string) http.Handler {
	t.Helper()
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
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'administrator','Admin','all',''),(2,'reseller','Reseller','own',''),(3,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'users.view','View','Admin')`)
	// admin and reseller hold users.view; a plain customer does not.
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(2,1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,parent_id) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'reseller','r@x','Reseller',?,2,1,'2026-01-01T00:00:00Z','','',NULL),
		(3,'subcust','s@x','Sub Customer',?,3,1,'2026-01-01T00:00:00Z','','',2),
		(4,'solocust','o@x','Solo Customer',?,3,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash, loginHash)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	c, tok := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {tok}, "username": {loginAs}, "password": {loginPassword}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d", loginAs, rr.Code)
	}
	// Attach the auth cookie to the handler via a wrapper so callers can reuse.
	t.Cleanup(func() {})
	return &loggedIn{h: h, cookie: rr.Result().Cookies()[0]}
}

// loggedIn carries an auth cookie so get() calls are authenticated.
type loggedIn struct {
	h      http.Handler
	cookie *http.Cookie
}

func (l *loggedIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.AddCookie(l.cookie)
	l.h.ServeHTTP(w, r)
}

func TestUsersResellerSeesOnlyOwnCustomers(t *testing.T) {
	h := usersEnv(t, "reseller")
	body := get(t, h, "/users", nil).Body.String()
	if !pageHas(body, "subcust") {
		t.Fatalf("reseller missing own customer:\n%s", body)
	}
	for _, other := range []string{"admin", "reseller", "solocust"} {
		if pageHas(body, ">"+other+"<") || pageHas(body, "<td>"+other+"</td>") {
			t.Fatalf("reseller saw a non-customer user %q:\n%s", other, body)
		}
	}
}

func TestUsersAdminSeesAll(t *testing.T) {
	h := usersEnv(t, "admin")
	body := get(t, h, "/users", nil).Body.String()
	for _, u := range []string{"admin", "reseller", "subcust", "solocust"} {
		if !pageHas(body, u) {
			t.Fatalf("admin missing user %s", u)
		}
	}
}

func TestUsersRequiresPermission(t *testing.T) {
	// A customer role without users.view must be refused.
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
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (3,'customer','Customer','own','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'cust','c@x','Cust',?,3,1,'2026-01-01T00:00:00Z','','')`, loginHash)
	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	hh := s.Handler()
	c, tok := getLogin(t, hh)
	rr := postForm(hh, "/login", url.Values{"_token": {tok}, "username": {"cust"}, "password": {loginPassword}}, c)
	cookie := rr.Result().Cookies()[0]
	if got := get(t, hh, "/users", cookie); got.Code != http.StatusForbidden {
		t.Fatalf("/users without users.view = %d, want 403", got.Code)
	}
}

// A reseller must not open the edit or plan page of a user that is not one
// of their customers (PHP answers "The user no longer exists").
func TestUsersResellerCannotOpenForeignUserPages(t *testing.T) {
	h, db := rolesEnv(t)
	if _, err := db.Exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,parent_id)
		VALUES (3,'res','res@x','Res',?,(SELECT id FROM roles WHERE name='reseller'),1,'2026-01-01T00:00:00Z','','',NULL),
		       (4,'own','own@x','Own',?,(SELECT id FROM roles WHERE name='customer'),1,'2026-01-01T00:00:00Z','','',3)`, loginHash, loginHash); err != nil {
		t.Fatal(err)
	}
	res := loginAsUser(t, h, "res")
	for _, path := range []string{"/users/1/edit", "/users/1/plan", "/users/2/edit"} { // admin, staff
		rr := get(t, h, path, res)
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/users" {
			t.Fatalf("GET %s = %d loc=%q, want 303 /users", path, rr.Code, rr.Header().Get("Location"))
		}
		if pageHas(rr.Body.String(), "a@x") || pageHas(rr.Body.String(), "m@x") {
			t.Fatalf("GET %s leaked the user's data", path)
		}
	}
	if rr := get(t, h, "/users/4/edit", res); rr.Code != http.StatusOK {
		t.Fatalf("own customer edit page = %d", rr.Code)
	}
}
