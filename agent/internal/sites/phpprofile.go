package sites

import (
	"regexp"
	"sort"
	"strings"
)

// PHP security profiles (items 33–35). A profile is a named disable_functions
// set so a website's dangerous-function policy is explicit and reviewable
// rather than an opaque boolean. "recommended" is the default for new
// websites; existing websites keep whatever they had (an empty Profile falls
// back to the legacy Harden flag, so nothing changes for them).
const (
	ProfileRecommended   = "recommended"
	ProfileBalanced      = "balanced"
	ProfileCompatibility = "compatibility"
	ProfileCustom        = "custom"
)

// legacyHardenList is exactly what Harden disabled before profiles existed;
// it is kept so an existing website with Harden set is byte-for-byte
// unchanged.
const legacyHardenList = "exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec"

var profileLists = map[string]string{
	// The full dangerous set from the GA brief.
	ProfileRecommended: "exec,passthru,shell_exec,system,proc_open,popen,proc_get_status,proc_terminate,pcntl_exec,pcntl_fork,pcntl_signal,pcntl_alarm,pcntl_wait",
	// The command-execution family, but process-control functions a queue
	// worker or framework may legitimately use are left enabled.
	ProfileBalanced: "exec,passthru,shell_exec,system,proc_open,popen",
	// Nothing disabled.
	ProfileCompatibility: "",
}

var validProfiles = map[string]bool{
	ProfileRecommended: true, ProfileBalanced: true, ProfileCompatibility: true, ProfileCustom: true,
}

var phpFuncRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,63}$`)

// DisableFunctionsFor returns the disable_functions value for a website's PHP
// settings. An empty Profile preserves the legacy behaviour.
func DisableFunctionsFor(p PHPSettings) string {
	switch p.Profile {
	case "":
		if p.Harden {
			return legacyHardenList
		}
		return ""
	case ProfileCustom:
		return normalizeFuncList(p.DisableFunctions)
	default:
		return profileLists[p.Profile]
	}
}

// normalizeFuncList cleans a custom list: lowercase function names, unique,
// sorted, anything that is not a function name dropped.
func normalizeFuncList(csv string) string {
	seen := map[string]bool{}
	var out []string
	for _, f := range strings.Split(csv, ",") {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" || seen[f] || !phpFuncRe.MatchString(f) {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
