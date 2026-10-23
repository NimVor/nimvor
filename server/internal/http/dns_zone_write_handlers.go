package http

// The DNS zone editor's write routes (DnsController::addRecord /
// updateRecord / deleteRecord / bulk / settings / sync / import / export /
// delete), all on the zone page. Rejected forms re-render the page in place
// with the field messages and the submitted values (the panel's
// backWithErrors, the record modal re-opens over them); everything else
// flashes and redirects, as in the PHP.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// showZoneOf resolves a scoped zone for a /dns/zones/{id} route (nil = not
// found or no access).
func (s *Server) showZoneOf(r *http.Request) *hosting.DnsZoneFull {
	user := userFrom(r.Context())
	z, err := hosting.NewDnsRepository(s.db).Zone(r.Context(), s.currentServerID(r), pathID(r), user)
	if err != nil {
		s.log.Error("show dns zone", "error", err)
		return nil
	}
	return z
}

// recordIDOf is the {record} path value (ok = a positive id).
func recordIDOf(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("record"), 10, 64)
	return id, err == nil && id > 0
}

// zonePageBack is the zone's page.
func zonePageBack(zone *hosting.DnsZoneFull) string { return fmt.Sprintf("/dns/zones/%d", zone.ID) }

// flashZoneError flashes a zone-op failure: the agent's message when the
// agent refused (PHP's AgentException is a UserFacingException), the
// service's user message, else a generic fallback; then redirects back.
func (s *Server) flashZoneError(w http.ResponseWriter, r *http.Request, back, fallback string, err error) {
	sess := sessionFrom(r.Context())
	var ae *agent.Error
	var ue *hosting.UserError
	switch {
	case errors.As(err, &ae):
		sess.Flash("error", ae.Message)
	case errors.As(err, &ue):
		sess.Flash("error", userFacingMsg(ue))
	default:
		s.log.Error("dns zone op", "error", err)
		sess.Flash("error", fallback)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// zoneRecordForm is the record modal's submitted values, defaulted (the
// template renders it after a refused save, as PHP's $old).
type zoneRecordForm struct {
	Record      string
	Type        string
	Name        string
	TTL         string
	SrvService  string
	SrvProtocol string
	Priority    string
	Weight      string
	Port        string
	Value       string
	CaaFlags    string
	CaaTag      string
	CaaValue    string
}

// zoneRecordFormOf is the record modal's values after a refused save: the
// submitted ones, with the modal defaults where the form was silent (the
// JS re-opens the modal over them, as in PHP's $old).
func zoneRecordFormOf(in map[string]string) zoneRecordForm {
	f := zoneRecordForm{
		Record: "0", Type: "A", Name: "@", TTL: "0", Priority: "10",
	}
	if in == nil {
		return f
	}
	get := func(k string) (string, bool) { return in[k], in[k] != "" }
	if v, ok := get("_record"); ok {
		f.Record = v
	}
	f.Type = in["type"]
	if f.Type == "" {
		f.Type = "A"
	}
	f.Name = in["name"]
	if f.Name == "" {
		f.Name = "@"
	}
	if v, ok := get("ttl"); ok {
		f.TTL = v
	}
	f.SrvService = in["srv_service"]
	f.SrvProtocol = in["srv_protocol"]
	if v, ok := get("priority"); ok {
		f.Priority = v
	}
	f.Weight = in["weight"]
	f.Port = in["port"]
	f.Value = in["value"]
	// PHP: $fv('caa_flags', $fv('priority')).
	if v, ok := get("caa_flags"); ok {
		f.CaaFlags = v
	} else if v, ok := get("priority"); ok {
		f.CaaFlags = v
	} else {
		f.CaaFlags = "0"
	}
	f.CaaTag = in["caa_tag"]
	f.CaaValue = in["caa_value"]
	if f.CaaTag == "" {
		// The CAA value's two words, as the PHP's explode.
		if sp := strings.Index(f.Value, " "); sp > 0 {
			f.CaaTag = f.Value[:sp]
			f.CaaValue = strings.TrimSpace(f.Value[sp+1:])
		} else {
			f.CaaTag = f.Value
		}
	}
	return f
}

// zoneShowData builds the zone page's context (the read-only state plus,
// after a refused save, the field errors and the submitted values).
func (s *Server) zoneShowData(r *http.Request, zone *hosting.DnsZoneFull, records []hosting.DnsRecord,
	errs map[string]string, in map[string]string, importSkipped []hosting.ImportSkipped, sort string) map[string]any {
	sess := sessionFrom(r.Context())
	u := userFrom(r.Context())
	servedState, servedSerial := "none", 0
	if st, err := s.siteDnsService(r, u).Status(r.Context()); err == nil && st != nil {
		if se := st.ServedSerial(zone.Name); se != nil {
			servedState, servedSerial = "pending", *se
			if *se == zone.Serial {
				servedState = "live"
			}
		}
	}
	warnings := hosting.Warnings(zone.Name, records)
	var site *hosting.Website
	if zone.WebsiteID.Valid {
		site, _ = hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), zone.WebsiteID.Int64, u)
	}
	form := zoneRecordFormOf(in)
	// The edit buttons carry the record as JSON (the shared JS's contract).
	editJSON := make([]string, len(records))
	for i, rec := range records {
		b, _ := json.Marshal(map[string]any{
			"id": rec.ID, "type": rec.Type, "name": rec.Name, "value": rec.Value,
			"ttl": rec.TTL, "priority": rec.Priority, "weight": rec.Weight, "port": rec.Port,
		})
		editJSON[i] = string(b)
	}
	reopen := ""
	if len(errs) > 0 && in != nil && in["type"] != "" {
		if form.Record != "" && form.Record != "0" {
			reopen = fmt.Sprintf("/dns/zones/%d/records/%s", zone.ID, form.Record)
		} else {
			reopen = fmt.Sprintf("/dns/zones/%d/records", zone.ID)
		}
	}
	// The settings card's values (the submitted ones when refused).
	ttlValue := fmt.Sprint(zone.TTL)
	secondariesValue := strings.ReplaceAll(zone.Secondaries, ",", ", ")
	if in != nil {
		if v, ok := in["ttl"]; ok {
			ttlValue = v
		}
		if v, ok := in["secondaries"]; ok {
			secondariesValue = v
		}
	}
	confirmValue := ""
	if in != nil {
		confirmValue = in["confirm"]
	}
	return map[string]any{
		"User":         u,
		"CSRF":         session.CSRFToken(sess),
		"Zone":         zone,
		"Records":      records,
		"EditJSON":     editJSON,
		"Types":        hosting.DnsRecordTypes,
		"SrvProtocols": []string{"tcp", "udp", "tls"},
		"ServedState":  servedState,
		"ServedSerial": servedSerial,
		"Warnings":     warnings,
		"Site":         site,
		"Admin":        hosting.IsAll(u),
		"Errors":       errs,
		"Form":         form,
		"Reopen":       reopen,
		"TTLValue":     ttlValue,
		"Secondaries":  secondariesValue,
		"Confirm":      confirmValue,
		"Zonefile":     formZonefile(in),
		"ImportSkipped": importSkipped,
		"Sort":         sort,
		"Success":      pullFlashKey(sess, "success"),
		"Error":        pullFlashKey(sess, "error"),
	}
}

// formZonefile is the import box's old text (a refused import re-renders
// with it).
func formZonefile(in map[string]string) string {
	if in == nil {
		return ""
	}
	return in["zonefile"]
}

// renderZone renders the zone page; errs/in re-open the record form (the
// panel's backWithErrors).
func (s *Server) renderZone(w http.ResponseWriter, r *http.Request, zone *hosting.DnsZoneFull,
	records []hosting.DnsRecord, errs map[string]string, in map[string]string, importSkipped []hosting.ImportSkipped, sort string) {
	s.render(w, "dns_zone", s.zoneShowData(r, zone, records, errs, in, importSkipped, sort))
}

// zoneRecords loads a zone's records in the display order, optionally
// re-sorted like the PHP's sort= query.
func (s *Server) zoneRecords(ctx context.Context, zoneID int64, sort string) []hosting.DnsRecord {
	records, err := hosting.NewDnsRepository(s.db).Records(ctx, zoneID)
	if err != nil {
		s.log.Error("list dns records", "zone", zoneID, "error", err)
		return []hosting.DnsRecord{}
	}
	switch sort {
	case "name", "type", "ttl", "value":
		sortRecords(records, sort)
	}
	return records
}

// sortRecords is the PHP's usort: the chosen column, name as the tiebreak.
func sortRecords(records []hosting.DnsRecord, sort string) {
	for i := 1; i < len(records); i++ {
		for j := i; j > 0; j-- {
			if recCmp(records[j-1], records[j], sort) <= 0 {
				break
			}
			records[j-1], records[j] = records[j], records[j-1]
		}
	}
}

// recCmp is the PHP's comparator: the chosen column, the name as tiebreak
// (the strcmp is byte-order, as in the PHP).
func recCmp(a, b hosting.DnsRecord, sort string) int {
	var c int
	switch sort {
	case "ttl":
		c = a.TTL - b.TTL
	case "type":
		c = strings.Compare(a.Type, b.Type)
	case "value":
		c = strings.Compare(a.Value, b.Value)
	default:
		c = strings.Compare(a.Name, b.Name)
	}
	if c == 0 && sort != "name" {
		c = strings.Compare(a.Name, b.Name)
	}
	return c
}

// ---- the record routes ----------------------------------------------------

// dnsRecordAdd adds a record (DnsController::addRecord, route
// dns.records.store, dns.manage).
func (s *Server) dnsRecordAdd(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	if !s.checkZoneCSRF(w, r) {
		return
	}
	in := postValues(r)
	if err := s.siteDnsService(r, user).SaveRecord(r.Context(), zone, nil, in, false); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.renderZone(w, r, zone, s.zoneRecords(r.Context(), zone.ID, ""), ve.Fields, in, nil, "")
			return
		}
		s.flashZoneError(w, r, zonePageBack(zone), "The record could not be saved.", err)
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Record added; the zone was reloaded.")
	http.Redirect(w, r, zonePageBack(zone), http.StatusSeeOther)
}

// dnsRecordUpdate changes a record (DnsController::updateRecord, route
// dns.records.update, dns.manage).
func (s *Server) dnsRecordUpdate(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	recordID, ok := recordIDOf(r)
	if !ok {
		s.zoneNotFound(w, r)
		return
	}
	if !s.checkZoneCSRF(w, r) {
		return
	}
	in := postValues(r)
	id := recordID
	if err := s.siteDnsService(r, user).SaveRecord(r.Context(), zone, &id, in, false); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.renderZone(w, r, zone, s.zoneRecords(r.Context(), zone.ID, ""), ve.Fields, in, nil, "")
			return
		}
		s.flashZoneError(w, r, zonePageBack(zone), "The record could not be saved.", err)
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Record saved; the zone was reloaded.")
	http.Redirect(w, r, zonePageBack(zone), http.StatusSeeOther)
}

// dnsRecordDelete removes a record (DnsController::deleteRecord, route
// dns.records.delete, dns.manage).
func (s *Server) dnsRecordDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	recordID, ok := recordIDOf(r)
	if !ok {
		s.zoneNotFound(w, r)
		return
	}
	if !s.checkZoneCSRF(w, r) {
		return
	}
	id := recordID
	if err := s.siteDnsService(r, user).SaveRecord(r.Context(), zone, &id, nil, true); err != nil {
		s.flashZoneError(w, r, zonePageBack(zone), "The record could not be deleted.", err)
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Record deleted; the zone was reloaded.")
	http.Redirect(w, r, zonePageBack(zone), http.StatusSeeOther)
}

// dnsBulk enables, disables or deletes the selected records (DnsController::
// bulk, route dns.bulk, dns.manage).
func (s *Server) dnsBulk(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	if !s.checkZoneCSRF(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ids := []int64{}
	for _, v := range r.PostForm["ids[]"] {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, n)
		}
	}
	n, err := s.siteDnsService(r, user).Bulk(r.Context(), zone, ids, r.PostFormValue("op"))
	if err != nil {
		s.flashZoneError(w, r, zonePageBack(zone), "The records could not be updated.", err)
		return
	}
	verb := map[string]string{"enable": "turned on", "disable": "turned off", "delete": "deleted"}[r.PostFormValue("op")]
	if verb == "" {
		verb = "updated"
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", fmt.Sprintf("%d record(s) %s; the zone was reloaded.", n, verb))
	http.Redirect(w, r, zonePageBack(zone), http.StatusSeeOther)
}

// dnsSettings saves the zone's default TTL and secondaries (DnsController::
// settings, route dns.settings, dns.manage).
func (s *Server) dnsSettings(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	if !s.checkZoneCSRF(w, r) {
		return
	}
	in := postValues(r)
	ttl := 3600
	if v, err := strconv.Atoi(in["ttl"]); err == nil {
		ttl = v
	}
	if err := s.siteDnsService(r, user).Settings(r.Context(), zone, ttl, in["secondaries"]); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.renderZone(w, r, zone, s.zoneRecords(r.Context(), zone.ID, ""), ve.Fields, in, nil, "")
			return
		}
		s.flashZoneError(w, r, zonePageBack(zone), "The zone settings could not be saved.", err)
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Zone settings saved.")
	http.Redirect(w, r, zonePageBack(zone), http.StatusSeeOther)
}

// dnsSync loads the zone again (DnsController::sync, route dns.sync,
// dns.manage).
func (s *Server) dnsSync(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	if !s.checkZoneCSRF(w, r) {
		return
	}
	if err := s.siteDnsService(r, user).Sync(r.Context(), zone); err != nil {
		s.flashZoneError(w, r, zonePageBack(zone), "The zone could not be loaded.", err)
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "The zone was loaded again.")
	http.Redirect(w, r, zonePageBack(zone), http.StatusSeeOther)
}

// dnsExport downloads the zone file (DnsController::export, route
// dns.export, dns.manage).
func (s *Server) dnsExport(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	text, err := s.siteDnsService(r, user).Export(r.Context(), zone.ID)
	if err != nil {
		s.flashZoneError(w, r, zonePageBack(zone), "The zone could not be exported.", err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", zone.Name+".zone"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(text))
}

// dnsImport adds a zone file's records (DnsController::import, route
// dns.import, dns.manage): the pasted text or an upload of up to 1 MB.
func (s *Server) dnsImport(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	if !s.checkZoneCSRF(w, r) {
		return
	}
	if err := r.ParseMultipartForm(1<<20); err != nil && err != http.ErrNotMultipart {
		// A plain form (the paste box) has no multipart parts.
		_ = r.ParseForm()
	}
	text := r.FormValue("zonefile")
	if text == "" {
		// The uploaded file, when present, wins (as in the PHP).
		if f, hdr, err := r.FormFile("zonefile_upload"); err == nil && hdr != nil && hdr.Size <= 1<<20 {
			b, err := io.ReadAll(io.LimitReader(f, 1<<20))
			f.Close()
			if err == nil {
				text = string(b)
			}
		}
	}
	res, err := s.siteDnsService(r, user).Import(r.Context(), zone, text)
	if err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.renderZone(w, r, zone, s.zoneRecords(r.Context(), zone.ID, ""), ve.Fields, postValues(r), nil, "")
			return
		}
		s.flashZoneError(w, r, zonePageBack(zone), "The zone file could not be imported.", err)
		return
	}
	msg := fmt.Sprintf("%d record(s) imported", res.Added)
	if res.Duplicates > 0 {
		msg += fmt.Sprintf(", %d already in the zone (name servers at @ are kept as they are)", res.Duplicates)
	}
	if len(res.Skipped) > 0 {
		msg += fmt.Sprintf("; %d line(s) skipped (listed below)", len(res.Skipped))
	}
	sess := sessionFrom(r.Context())
	if len(res.Skipped) > 0 {
		if b, err := json.Marshal(res.Skipped); err == nil {
			sess.Flash("dns_import", string(b))
		}
	}
	sess.Flash("success", msg+".")
	http.Redirect(w, r, zonePageBack(zone), http.StatusSeeOther)
}

// dnsZoneDelete removes the zone (DnsController::delete, route dns.delete,
// dns.manage); the zone name typed in confirms.
func (s *Server) dnsZoneDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone := s.showZoneOf(r)
	if zone == nil {
		s.zoneNotFound(w, r)
		return
	}
	if !s.checkZoneCSRF(w, r) {
		return
	}
	in := postValues(r)
	if err := s.siteDnsService(r, user).Delete(r.Context(), zone, in["confirm"]); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.renderZone(w, r, zone, s.zoneRecords(r.Context(), zone.ID, ""), ve.Fields, in, nil, "")
			return
		}
		s.flashZoneError(w, r, zonePageBack(zone), "The zone could not be deleted.", err)
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Zone "+zone.Name+" deleted.")
	http.Redirect(w, r, "/dns", http.StatusSeeOther)
}

// zoneNotFound is the write routes' scoped miss (the PHP's "Zone not
// found." back to the list).
func (s *Server) zoneNotFound(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", "Zone not found.")
	http.Redirect(w, r, "/dns", http.StatusSeeOther)
}

// checkZoneCSRF validates the zone forms' token.
func (s *Server) checkZoneCSRF(w http.ResponseWriter, r *http.Request) bool {
	if !session.ValidateCSRF(sessionFrom(r.Context()), r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}
