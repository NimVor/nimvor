package modules

import (
	"github.com/mchkhawarshahzad/whacp/agent/internal/adopt"
	"github.com/mchkhawarshahzad/whacp/agent/internal/apps"
	"github.com/mchkhawarshahzad/whacp/agent/internal/backup"
	"github.com/mchkhawarshahzad/whacp/agent/internal/cronmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/dbmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/deploy"
	"github.com/mchkhawarshahzad/whacp/agent/internal/dnsmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/files"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ftpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/logs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/mcmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/phpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/redismgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/security"
	"github.com/mchkhawarshahzad/whacp/agent/internal/services"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sites"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sslmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sysmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/usage"
	"github.com/mchkhawarshahzad/whacp/agent/internal/workers"
)

// All returns every feature module. Each module registers its own actions;
// modifying ones are marked Mutating and stay behind the write gate.
func All() []Module {
	return []Module{
		sites.Register,
		dbmgr.Register,
		sslmgr.Register,
		files.Register,
		logs.Register,
		cronmgr.Register,
		workers.Register,
		deploy.Register,
		redismgr.Register,
		mcmgr.Register,
		ftpmgr.Register,
		dnsmgr.Register,
		backup.Register,
		security.Register,
		sysmgr.Register,
		usage.Register,
		apps.Register,
		adopt.Register,
		phpmgr.Register,
		services.Register,
	}
}
