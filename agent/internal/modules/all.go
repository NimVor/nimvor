package modules

import (
	"github.com/mchkhawarshahzad/whacp/agent/internal/adopt"
	"github.com/mchkhawarshahzad/whacp/agent/internal/apps"
	"github.com/mchkhawarshahzad/whacp/agent/internal/avmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/backup"
	"github.com/mchkhawarshahzad/whacp/agent/internal/components"
	"github.com/mchkhawarshahzad/whacp/agent/internal/cronmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/dbmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/deploy"
	"github.com/mchkhawarshahzad/whacp/agent/internal/dnsmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ffmpegmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/files"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ftpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/hardening"
	"github.com/mchkhawarshahzad/whacp/agent/internal/hulk"
	"github.com/mchkhawarshahzad/whacp/agent/internal/limits"
	"github.com/mchkhawarshahzad/whacp/agent/internal/logs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/mailmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/mcmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/modsec"
	"github.com/mchkhawarshahzad/whacp/agent/internal/mongomgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/netmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/phpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/procmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/quota"
	"github.com/mchkhawarshahzad/whacp/agent/internal/redismgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/runtimemgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/security"
	"github.com/mchkhawarshahzad/whacp/agent/internal/services"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sites"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sslmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/statsmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sysmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/terminal"
	"github.com/mchkhawarshahzad/whacp/agent/internal/usage"
	"github.com/mchkhawarshahzad/whacp/agent/internal/webprotect"
	"github.com/mchkhawarshahzad/whacp/agent/internal/wordpress"
	"github.com/mchkhawarshahzad/whacp/agent/internal/workers"
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
	}
}
