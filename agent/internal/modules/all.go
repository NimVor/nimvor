package modules

import (
	"github.com/mchkhawarshahzad/whacp/agent/internal/dbmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/phpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/services"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sites"
)

// All returns every feature module. Each module registers its own actions;
// modifying ones are marked Mutating and stay behind the write gate.
func All() []Module {
	return []Module{
		sites.Register,
		dbmgr.Register,
		phpmgr.Register,
		services.Register,
	}
}
