// Command whacp-migrate inspects and applies the WhaCP database migrations.
//
//	whacp-migrate status   # list applied/pending
//	whacp-migrate up        # apply all pending migrations
//
// It shares the migrations ledger with the PHP panel (by migration name), so
// running it against an existing installation applies only new migrations.
package main

import (
	"fmt"
	"os"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func main() {
	cmd := "status"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	cfg := config.Load()
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open database:", err)
		os.Exit(1)
	}
	defer db.Close()

	switch cmd {
	case "status":
		applied, err := database.Applied(db)
		if err != nil {
			fail(err)
		}
		pending, err := database.Pending(db)
		if err != nil {
			fail(err)
		}
		fmt.Printf("database: %s\napplied:  %d\npending:  %d\n", cfg.DBPath, len(applied), len(pending))
		for _, p := range pending {
			fmt.Println("  +", p)
		}
	case "up":
		ran, err := database.Migrate(db)
		if err != nil {
			fail(err)
		}
		if len(ran) == 0 {
			fmt.Println("already up to date")
			return
		}
		fmt.Printf("applied %d migration(s):\n", len(ran))
		for _, r := range ran {
			fmt.Println("  +", r)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (use: status | up)\n", cmd)
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
