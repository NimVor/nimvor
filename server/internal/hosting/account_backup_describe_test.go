package hosting

import (
	"context"
	"testing"
)

// The description written into an account backup covers what the PHP panel
// describes: the user, websites, databases with grants, cron jobs, and one
// backup request per website.
func TestAccountBackupDescribe(t *testing.T) {
	db := writeDB(t)
	for _, q := range []string{
		`INSERT INTO databases (id,server_id,website_id,owner_id,name,created_at) VALUES (1,1,10,3,'cust_db','')`,
		`INSERT INTO database_users (id,server_id,owner_id,username,host,created_at) VALUES (1,1,3,'cust_u','localhost','')`,
		`INSERT INTO database_grants (database_id,user_id,privileges) VALUES (1,1,'all')`,
		`INSERT INTO cron_jobs (server_id,website_id,schedule,command,description,enabled,created_at,updated_at) VALUES (1,10,'* * * * *','php x.php','tick',1,'','')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewAccountBackupService(db, &fakeAgent{}, 1, nil)
	meta, reqs, err := svc.Describe(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	u := meta["user"].(map[string]any)
	if u["username"] != "cust" || u["plan"] != "Cust1" || u["parent"] != "reseller" || u["role"] != "customer" {
		t.Fatalf("user: %v", u)
	}
	if n := len(meta["websites"].([]map[string]any)); n != 1 || len(reqs) != 1 || reqs[0]["domain"] != "cust.example" || reqs[0]["user"] != "u_cust" {
		t.Fatalf("websites %d, requests %v", n, reqs)
	}
	if w := meta["websites"].([]map[string]any)[0]; w["type"] != "main" || w["databases"].([]string)[0] != "cust_db" {
		t.Fatalf("website: %v", w)
	}
	du := meta["database_users"].([]map[string]any)
	if len(du) != 1 || du[0]["grants"].([]map[string]any)[0]["database"] != "cust_db" {
		t.Fatalf("db users: %v", du)
	}
	if c := meta["cron_jobs"].([]map[string]any); len(c) != 1 || c[0]["command"] != "php x.php" {
		t.Fatalf("cron: %v", c)
	}
	if _, _, err := svc.Describe(context.Background(), 1); err == nil {
		t.Fatal("a staff user has no account backup")
	}
}
