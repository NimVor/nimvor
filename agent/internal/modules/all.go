package modules

import (
	"github.com/NimVor/nimvor/agent/internal/adopt"
	"github.com/NimVor/nimvor/agent/internal/apps"
	"github.com/NimVor/nimvor/agent/internal/avmgr"
	"github.com/NimVor/nimvor/agent/internal/backup"
	"github.com/NimVor/nimvor/agent/internal/components"
	"github.com/NimVor/nimvor/agent/internal/cronmgr"
	"github.com/NimVor/nimvor/agent/internal/dbmgr"
	"github.com/NimVor/nimvor/agent/internal/deploy"
	"github.com/NimVor/nimvor/agent/internal/dnsmgr"
	"github.com/NimVor/nimvor/agent/internal/ffmpegmgr"
	"github.com/NimVor/nimvor/agent/internal/files"
	"github.com/NimVor/nimvor/agent/internal/ftpmgr"
	"github.com/NimVor/nimvor/agent/internal/hardening"
	"github.com/NimVor/nimvor/agent/internal/hulk"
	"github.com/NimVor/nimvor/agent/internal/limits"
	"github.com/NimVor/nimvor/agent/internal/logs"
	"github.com/NimVor/nimvor/agent/internal/mailmgr"
	"github.com/NimVor/nimvor/agent/internal/mcmgr"
	"github.com/NimVor/nimvor/agent/internal/modsec"
	"github.com/NimVor/nimvor/agent/internal/mongomgr"
	"github.com/NimVor/nimvor/agent/internal/netmgr"
	"github.com/NimVor/nimvor/agent/internal/nginxmgr"
	"github.com/NimVor/nimvor/agent/internal/phpmgr"
	"github.com/NimVor/nimvor/agent/internal/procmgr"
	"github.com/NimVor/nimvor/agent/internal/quota"
	"github.com/NimVor/nimvor/agent/internal/redismgr"
	"github.com/NimVor/nimvor/agent/internal/runtimemgr"
	"github.com/NimVor/nimvor/agent/internal/security"
	"github.com/NimVor/nimvor/agent/internal/services"
	"github.com/NimVor/nimvor/agent/internal/sites"
	"github.com/NimVor/nimvor/agent/internal/sslmgr"
	"github.com/NimVor/nimvor/agent/internal/statsmgr"
	"github.com/NimVor/nimvor/agent/internal/sysmgr"
	"github.com/NimVor/nimvor/agent/internal/terminal"
	"github.com/NimVor/nimvor/agent/internal/usage"
	"github.com/NimVor/nimvor/agent/internal/webprotect"
	"github.com/NimVor/nimvor/agent/internal/wordpress"
	"github.com/NimVor/nimvor/agent/internal/workers"
)

// All returns every feature module. Each module registers its own actions;
// modifying ones are marked Mutating and stay behind the write gate.
func All() []Module {
	return []Module{
		sites.Register,
		dbmgr.Register,
		sslmgr.Register,
		modsec.Register,
		webprotect.Register,
		files.Register,
		logs.Register,
		cronmgr.Register,
		statsmgr.Register,
		avmgr.Register,
		components.Register,
		ffmpegmgr.Register,
		hardening.Register,
		hulk.Register,
		netmgr.Register,
		limits.Register,
		quota.Register,
		workers.Register,
		deploy.Register,
		redismgr.Register,
		mcmgr.Register,
		ftpmgr.Register,
		dnsmgr.Register,
		mailmgr.Register,
		backup.Register,
		security.Register,
		sysmgr.Register,
		terminal.Register,
		usage.Register,
		apps.Register,
		wordpress.Register,
		adopt.Register,
		phpmgr.Register,
		services.Register,
		procmgr.Register,
		runtimemgr.Register,
		mongomgr.Register,
		nginxmgr.Register,
	}
}
