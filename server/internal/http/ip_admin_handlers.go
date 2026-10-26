// This file ports the /ips area for server administrators (IpController):
// the address pool (add / edit / delete / rebuild), the routed IPv6 ranges
// (add / note / remove / allocate) and the migration wizard (plan + run).
// Like the PHP controller it is only for server administrators
// (Scope::isAll), on top of the route permissions.
package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// ipAdminGate keeps the /ips area to server administrators (the PHP
// Scope::isAll denial, 403 page).
func (s *Server) ipAdminGate(w http.ResponseWriter, r *http.Request) bool {
	if !hosting.IsAll(userFrom(r.Context())) {
		renderStatus(w, s, "error", map[string]any{
			"Title":   "Access denied",
			"Message": "IP addresses are managed by server administrators.",
		}, http.StatusForbidden)
		return false
	}
	return true
}

// postCSRF parses the request form and checks its CSRF token (the PHP
// VerifyCsrfToken middleware on these POSTs); it reports whether the
// request may proceed.
func postCSRF(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return false
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// ipAgentErr is the page's agent-error message (the PHP agentError card).
func ipAgentErr(err error) string {
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return "The WhaCP agent could not be reached."
}

// firstValidationMessage is the first field message in field order (these
// pages flash the message instead of re-rendering the form, so the field
// text is more useful than the generic "correct the fields" line).
func firstValidationMessage(ve *hosting.ValidationError) string {
	keys := make([]string, 0, len(ve.Fields))
	for k := range ve.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ve.Fields[k] != "" {
			return ve.Fields[k]
		}
	}
	return "Please correct the highlighted fields."
}

// ipAct runs one pool write action and lands the flash + redirect (the
// PHP act(): validation errors flash the first field message, user errors
// their message, success fn's message).
func (s *Server) ipAct(w http.ResponseWriter, r *http.Request, to string, fn func() (string, error)) {
	sess := sessionFrom(r.Context())
	msg, err := fn()
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			sess.Flash("error", firstValidationMessage(ve))
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("ip admin action", "error", err)
			sess.Flash("error", "The change could not be made.")
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ipSiteT is a pool site row for the /ips views.
type ipSiteT struct {
	ID     int64
	Domain string
	Owner  string
}

// ipPoolRowT is one pool row shaped for the pool page.
type ipPoolRowT struct {
	ID           int64
	Address      string
	Family       int
	KindLabel    string
	Reserved     bool
	Reason       string
	FromRange    bool
	Sites        []ipSiteT
	InUse        bool
	State        string
	PresentLabel string
	PresentClass string
}

// ipUnpooledT is a usable server address that is not in the pool.
type ipUnpooledT struct {
	Address   string
	Interface string
	PrefixLen int
}

// ipRebuildFlash is the rebuild result flashed for the next pool render.
type ipRebuildFlash struct {
	Added   []string `json:"added"`
	Missing []string `json:"missing"`
}

// ipIndex renders the pool page (IpController::index).
func (s *Server) ipIndex(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	svc := s.ipService(r, user)
	ctx := r.Context()

	var agentErr string
	var server *hosting.IpServer
	srv, err := svc.ServerAddresses(ctx)
	if err != nil {
		agentErr = ipAgentErr(err)
	} else {
		server = srv
	}

	pool, err := svc.PoolFull(ctx)
	if err != nil {
		s.log.Error("ip pool", "error", err)
		sess.Flash("error", "The pool could not be read.")
		http.Redirect(w, r, "/ips", http.StatusSeeOther)
		return
	}

	rows := make([]ipPoolRowT, 0, len(pool))
	inPool := make(map[string]bool, len(pool))
	for _, p := range pool {
		inPool[p.Address] = true
		sites := make([]ipSiteT, 0, len(p.Sites))
		for _, x := range p.Sites {
			sites = append(sites, ipSiteT{ID: x.ID, Domain: x.Domain, Owner: x.Owner})
		}
		inUse := len(sites) > 0
		reserved := p.Reserved == 1
		state := "free"
		if inUse {
			state = "used"
		}
		if reserved {
			state += " reserved"
		}
		if p.Kind == "dedicated" {
			state += " dedicated"
		}
		if p.Present != nil && !*p.Present {
			state += " missing"
		}
		var row ipPoolRowT
		row.ID = p.ID
		row.Address = p.Address
		row.Family = p.Family
		row.KindLabel = strings.ToUpper(p.Kind[:1]) + p.Kind[1:]
		row.Reserved = reserved
		row.Reason = p.Reason
		row.FromRange = p.RangeID != nil
		row.Sites = sites
		row.InUse = inUse
		row.State = strings.TrimSpace(state)
		row.PresentLabel = "unknown"
		if p.Present != nil {
			if *p.Present {
				row.PresentLabel, row.PresentClass = "On the server", "wcp-state is-ok"
			} else {
				row.PresentLabel, row.PresentClass = "Not on the server", "wcp-state is-error"
			}
		}
		rows = append(rows, row)
	}

	unpooled := []ipUnpooledT{}
	if server != nil {
		for _, a := range server.Addresses {
			ip := hosting.Canonical(a.Address)
			if ip == "" || !hosting.Usable(ip) || a.Scope == "link" || inPool[ip] {
				continue
			}
			unpooled = append(unpooled, ipUnpooledT{Address: ip, Interface: a.Interface, PrefixLen: a.PrefixLen})
		}
	}
	gateways := []string{}
	if server != nil {
		gateways = server.Gateways
	}

	var rebuild *ipRebuildFlash
	if v, ok := sess.PullFlash("ip_rebuild"); ok {
		var rf ipRebuildFlash
		if err := json.Unmarshal([]byte(v), &rf); err == nil {
			rebuild = &rf
		}
	}

	s.render(w, "ips_index", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Tab":       "addresses",
		"Pool":      rows,
		"Unpooled":  unpooled,
		"Server":    server,
		"Gateways":  gateways,
		"AgentErr":  agentErr,
		"Rebuild":   rebuild,
		"CanManage": s.can(r, "server.manage"),
		"Success":   pullFlashKey(sess, "success"),
		"Error":     pullFlashKey(sess, "error"),
	})
}

// ipStore pools an address of the server (IpController::store).
func (s *Server) ipStore(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	kind := r.PostFormValue("kind")
	if kind == "" {
		kind = "shared"
	}
	s.ipAct(w, r, "/ips", func() (string, error) {
		if err := s.ipService(r, user).Add(r.Context(), r.PostFormValue("address"), kind, r.PostFormValue("reason")); err != nil {
			return "", err
		}
		return "Address added to the pool.", nil
	})
}

// ipUpdate saves a pool row's assignment (IpController::update).
func (s *Server) ipUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	kind := r.PostFormValue("kind")
	if kind == "" {
		kind = "shared"
	}
	s.ipAct(w, r, "/ips", func() (string, error) {
		if err := s.ipService(r, user).UpdatePoolRow(r.Context(), id, kind, r.PostFormValue("reserved") == "1", r.PostFormValue("reason")); err != nil {
			return "", err
		}
		return "Address saved.", nil
	})
}

// ipDelete removes an unused pool row (IpController::delete).
func (s *Server) ipDelete(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	s.ipAct(w, r, "/ips", func() (string, error) {
		if err := s.ipService(r, user).DeletePoolRow(r.Context(), id); err != nil {
			return "", err
		}
		return "Address removed from the pool (the server keeps it on its interface).", nil
	})
}

// ipRebuild reconciles the pool with the server (IpController::rebuild).
func (s *Server) ipRebuild(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	sess := sessionFrom(r.Context())
	s.ipAct(w, r, "/ips", func() (string, error) {
		added, missing, err := s.ipService(r, user).Rebuild(r.Context())
		if err != nil {
			return "", err
		}
		if b, err := json.Marshal(ipRebuildFlash{Added: added, Missing: missing}); err == nil {
			sess.Flash("ip_rebuild", string(b))
		}
		msg := fmt.Sprintf("Pool rebuilt: %d address(es) added", len(added))
		if len(missing) > 0 {
			msg += fmt.Sprintf(", %d no longer on the server (kept, listed below)", len(missing))
		}
		return msg + ".", nil
	})
}

// ipRangeT is one IPv6 range card for the ranges page.
type ipRangeT struct {
	ID        int64
	Prefix    string
	Note      string
	Allocated int
	Used      int
	Mine      []ipAllocT
	HasMine   bool
	CanRemove bool
}

// ipAllocT is one address allocated out of a range.
type ipAllocT struct {
	Address string
	Domains string
	Free    bool
}

// ipSiteOption is a website choice for the allocation form.
type ipSiteOption struct {
	ID     int64
	Domain string
}

// ipRanges renders the ranges page (IpController::ranges).
func (s *Server) ipRanges(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	svc := s.ipService(r, user)
	ctx := r.Context()

	ranges, err := svc.Ranges(ctx)
	if err != nil {
		s.log.Error("ip ranges", "error", err)
		sess.Flash("error", "The ranges could not be read.")
		http.Redirect(w, r, "/ips/ranges", http.StatusSeeOther)
		return
	}
	pool, err := svc.Pool(ctx)
	if err != nil {
		s.log.Error("ip pool", "error", err)
		sess.Flash("error", "The pool could not be read.")
		http.Redirect(w, r, "/ips/ranges", http.StatusSeeOther)
		return
	}
	sites, err := hosting.NewWebsiteRepository(s.db).All(ctx, s.currentServerID(r), user)
	if err != nil {
		s.log.Error("ip ranges websites", "error", err)
		sess.Flash("error", "The websites could not be read.")
		http.Redirect(w, r, "/ips/ranges", http.StatusSeeOther)
		return
	}

	byRange := map[int64][]hosting.IPAddress{}
	for _, a := range pool {
		byRange[a.ID] = append(byRange[a.ID], a)
	}
	rows := make([]ipRangeT, 0, len(ranges))
	for _, g := range ranges {
		var row ipRangeT
		row.ID = g.ID
		row.Prefix = g.Prefix
		row.Note = g.Note
		row.Allocated = g.Allocated
		row.Used = g.Used
		row.CanRemove = g.Used == 0
		row.Mine = []ipAllocT{}
		for _, a := range byRange[g.ID] {
			domains := []string{}
			for _, x := range a.Sites {
				domains = append(domains, x.Domain)
			}
			row.Mine = append(row.Mine, ipAllocT{Address: a.Address, Domains: strings.Join(domains, ", "), Free: len(a.Sites) == 0})
		}
		row.HasMine = len(row.Mine) > 0
		rows = append(rows, row)
	}

	websites := make([]ipSiteOption, 0, len(sites))
	for _, w := range sites {
		websites = append(websites, ipSiteOption{ID: w.ID, Domain: w.Domain})
	}

	s.render(w, "ips_ranges", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Tab":       "ranges",
		"Ranges":    rows,
		"Websites":  websites,
		"CanManage": s.can(r, "server.manage"),
		"Success":   pullFlashKey(sess, "success"),
		"Error":     pullFlashKey(sess, "error"),
	})
}

// ipStoreRange routes a new IPv6 range (IpController::storeRange).
func (s *Server) ipStoreRange(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	s.ipAct(w, r, "/ips/ranges", func() (string, error) {
		if err := s.ipService(r, user).AddRange(r.Context(), r.PostFormValue("prefix"), r.PostFormValue("note")); err != nil {
			return "", err
		}
		return "Range added; its addresses are now local to this server.", nil
	})
}

// ipUpdateRange saves a range's note (IpController::updateRange).
func (s *Server) ipUpdateRange(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	s.ipAct(w, r, "/ips/ranges", func() (string, error) {
		if err := s.ipService(r, user).UpdateRange(r.Context(), id, r.PostFormValue("note")); err != nil {
			return "", err
		}
		return "Range saved.", nil
	})
}

// ipDeleteRange removes a range and its free allocations
// (IpController::deleteRange).
func (s *Server) ipDeleteRange(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	s.ipAct(w, r, "/ips/ranges", func() (string, error) {
		if err := s.ipService(r, user).RemoveRange(r.Context(), id); err != nil {
			return "", err
		}
		return "Range removed.", nil
	})
}

// ipAllocate takes an address of a range into the pool, optionally
// assigning it straight to a website (IpController::allocate).
func (s *Server) ipAllocate(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	siteID, _ := strconv.ParseInt(r.PostFormValue("website_id"), 10, 64)
	s.ipAct(w, r, "/ips/ranges", func() (string, error) {
		svc := s.ipService(r, user)
		ip, err := svc.Allocate(r.Context(), id, r.PostFormValue("address"), r.PostFormValue("kind"))
		if err != nil {
			return "", err
		}
		if siteID > 0 {
			site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), siteID, user)
			if errors.Is(err, sql.ErrNoRows) {
				site, err = nil, nil // FindFull reports a missing (or not visible) website as ErrNoRows
			}
			if err != nil {
				return "", err
			}
			if site == nil {
				return "", &hosting.UserError{What: "That website does not exist."}
			}
			n, err := svc.Assign(r.Context(), site, site.IPv4.String, ip, r.PostFormValue("dns") == "1")
			if err != nil {
				return "", err
			}
			msg := ip + " allocated and assigned to " + site.Domain
			if n > 0 {
				msg += fmt.Sprintf("; %d DNS record(s) updated", n)
			}
			return msg + ".", nil
		}
		return ip + " allocated.", nil
	})
}

// ipMigrateQuery is the wizard's submitted query (shown again on the page).
type ipMigrateQuery struct {
	Source   string
	Dest     string
	AllZones bool
}

// ipMigratePage renders the migration wizard (IpController::migrate).
func (s *Server) ipMigratePage(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	svc := s.ipService(r, user)
	ctx := r.Context()

	query := ipMigrateQuery{
		Source:   r.URL.Query().Get("source"),
		Dest:     r.URL.Query().Get("dest"),
		AllZones: r.URL.Query().Get("all_zones") == "1",
	}
	var plan *hosting.IpMigrationPlan
	errs := map[string]string{}
	if query.Source != "" || query.Dest != "" {
		p, err := svc.MigrationPlan(ctx, query.Source, query.Dest, query.AllZones)
		if err != nil {
			var ve *hosting.ValidationError
			if errors.As(err, &ve) {
				errs = ve.Fields
			} else {
				s.log.Error("ip migration plan", "error", err)
				errs["source"] = "The changes could not be worked out."
			}
		} else {
			plan = p
		}
	}

	pool, err := svc.Pool(ctx)
	if err != nil {
		s.log.Error("ip pool", "error", err)
		sess.Flash("error", "The pool could not be read.")
		http.Redirect(w, r, "/ips/migrate", http.StatusSeeOther)
		return
	}
	offer := make([]hosting.IPAddress, 0, len(pool))
	for _, a := range pool {
		if a.Reserved == 0 || len(a.Sites) > 0 {
			offer = append(offer, a)
		}
	}

	var results []hosting.IpMigrationResult
	if v, ok := sess.PullFlash("ip_migration"); ok {
		if err := json.Unmarshal([]byte(v), &results); err != nil {
			results = nil
		}
	}

	s.render(w, "ips_migrate", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Tab":       "migrate",
		"Pool":      offer,
		"Plan":      plan,
		"Errors":    errs,
		"Query":     &query,
		"Results":   results,
		"CanManage": s.can(r, "server.manage"),
		"Success":   pullFlashKey(sess, "success"),
		"Error":     pullFlashKey(sess, "error"),
	})
}

// ipRunMigration moves the websites (and, asked for, the DNS records) to
// the destination address (IpController::runMigration).
func (s *Server) ipRunMigration(w http.ResponseWriter, r *http.Request) {
	if !s.ipAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !postCSRF(w, r) {
		return
	}
	sess := sessionFrom(r.Context())
	if r.PostFormValue("confirm") != "1" {
		sess.Flash("error", "Confirm the migration first.")
		http.Redirect(w, r, "/ips/migrate", http.StatusSeeOther)
		return
	}
	results, err := s.ipService(r, user).Migrate(r.Context(),
		r.PostFormValue("source"), r.PostFormValue("dest"), r.PostFormValue("all_zones") == "1")
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			sess.Flash("error", firstValidationMessage(ve))
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("ip migration", "error", err)
			sess.Flash("error", "The migration could not be started.")
		}
		http.Redirect(w, r, "/ips/migrate", http.StatusSeeOther)
		return
	}
	if b, err := json.Marshal(results); err == nil {
		sess.Flash("ip_migration", string(b))
	}
	failed := 0
	for _, x := range results {
		if !x.OK {
			failed++
		}
	}
	if failed > 0 {
		sess.Flash("error", fmt.Sprintf("%d step(s) failed; see the results.", failed))
	} else {
		sess.Flash("success", "Migration complete.")
	}
	http.Redirect(w, r, "/ips/migrate", http.StatusSeeOther)
}
