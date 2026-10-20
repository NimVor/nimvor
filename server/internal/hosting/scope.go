// Package hosting holds row-level visibility for hosting objects — the tenant
// isolation primitive. It is the Go port of WhaCP\Hosting\Scope.
//
// Roles with scope "all" (staff) see every object on the server. Roles with
// scope "own" (resellers, customers) see objects they own and, for resellers,
// objects owned by their customers (users whose parent_id is the reseller).
// Visibility is always derived from the authenticated principal and
// server-side state — never from a client-supplied owner/tenant id.
package hosting

import "github.com/mchkhawarshahzad/whacp/server/internal/identity"

// Condition returns an SQL predicate (with positional ? placeholders) and the
// args to splice into the surrounding query, scoping ownerColumn to what the
// user may see. Insert the fragment as a contiguous unit and append its args
// at the matching position; the args are returned left-to-right.
//
// A nil user is treated as "own" with id 0, which matches nothing — fail
// closed. Callers that must reject anonymous access do so before querying.
func Condition(u *identity.User, ownerColumn string) (string, []any) {
	if u != nil && u.RoleScope == "all" {
		return "1 = 1", nil
	}
	var id int64
	if u != nil {
		id = u.ID
	}
	return "(" + ownerColumn + " = ? OR " + ownerColumn +
		" IN (SELECT id FROM users WHERE parent_id = ?))", []any{id, id}
}

// WithWebsite is like Condition, but an object attached to a website is also
// visible to whoever may see that website (e.g. the DNS zone an administrator
// created for a customer's website). Ported from Scope::withWebsite.
func WithWebsite(u *identity.User, ownerColumn, websiteColumn string) (string, []any) {
	cond, params := Condition(u, ownerColumn)
	if cond == "1 = 1" {
		return cond, params
	}
	siteCond, siteParams := Condition(u, "owner_id")
	return "(" + cond + " OR " + websiteColumn + " IN (SELECT id FROM websites WHERE " + siteCond + "))",
		append(params, siteParams...)
}

// IsAll reports whether the user has server-wide scope.
func IsAll(u *identity.User) bool { return u != nil && u.RoleScope == "all" }
