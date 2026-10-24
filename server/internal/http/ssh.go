package http

// This file ports the SSH area (SecurityController::ssh et al.): the SSH
// server settings (the guarded ssh.configure), the administrator key list
// (ssh.keys), the key changes — add an existing public key, generate a key
// pair in the panel (the private key is shown one time, never stored),
// remove by fingerprint — all administrators-only (role_name admin), and
// the once-shown private-key page (no-store).

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// sshRootLabels is the view's $rootLabels.
var sshRootLabels = map[string]string{
	"prohibit-password":   "With SSH keys only",
	"no":                  "Not allowed",
	"yes":                 "With password or key (not recommended)",
	"forced-commands-only": "Forced commands only",
}

// sshKeyAccount is one administrator row of the key table.
type sshKeyAccount struct {
	User       string
	Keys       []sshKeyRow
	OtherLines int
}

// sshKeyRow is one public key of an administrator.
type sshKeyRow struct {
	Type        string
	Bits        int
	Comment     string
	Fingerprint string
	Options     bool
}

// sshIndex renders the SSH page (SecurityController::ssh).
func (s *Server) sshIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	var st struct {
		Effective map[string]string `json:"effective"`
		RootKeys  bool              `json:"root_has_keys"`
		Admins    []string          `json:"admins_with_keys"`
		Managed   string            `json:"managed_file"`
	}
	agentMsg := ""
	var ae *agent.Error
	if res, err := s.agent.Call(r.Context(), "ssh.status", nil); err != nil {
		if errors.As(err, &ae) && ae.Message != "" {
			agentMsg = ae.Message
		} else {
			s.log.Error("ssh status", "error", err)
			agentMsg = "The WhaCP agent could not be reached."
		}
	} else if err := json.Unmarshal(res, &st); err != nil {
		s.log.Error("ssh status", "error", err)
		agentMsg = "The WhaCP agent answered with an unreadable state."
	}
	// The administrators' keys (agents before key management: none).
	keys := []sshKeyAccount{}
	if res, err := s.agent.Call(r.Context(), "ssh.keys", nil); err == nil {
		var ks struct {
			Admins []struct {
				User  string `json:"user"`
				Keys  []struct {
					Type        string `json:"type"`
					Bits        int    `json:"bits"`
					Comment     string `json:"comment"`
					Fingerprint string `json:"fingerprint"`
					Options     bool   `json:"options"`
				} `json:"keys"`
				Other int `json:"other_lines"`
			} `json:"admins"`
		}
		if json.Unmarshal(res, &ks) == nil {
			for _, a := range ks.Admins {
				row := sshKeyAccount{User: a.User, OtherLines: a.Other}
				for _, k := range a.Keys {
					row.Keys = append(row.Keys, sshKeyRow{
						Type: k.Type, Bits: k.Bits, Comment: k.Comment,
						Fingerprint: k.Fingerprint, Options: k.Options,
					})
				}
				keys = append(keys, row)
			}
		}
	}

	eff := st.Effective
	root := eff["permitrootlogin"]
	if root == "without-password" {
		root = "prohibit-password"
	}
	if root == "" {
		root = "prohibit-password"
	}
	tries, _ := strconv.Atoi(eff["maxauthtries"])
	if eff["maxauthtries"] == "" {
		tries = 4
	}
	if tries < 1 {
		tries = 1
	}
	if tries > 10 {
		tries = 10
	}
	hasKey := st.RootKeys || len(st.Admins) > 0
	installed := []string{}
	if st.RootKeys {
		installed = append(installed, "root")
	}
	installed = append(installed, st.Admins...)
	ok, _ := s.authorizer().Can(r.Context(), user, "security.manage")
	data := map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sess),
		"SSH":           nil,
		"Keys":          keys,
		"KeysInstalled": strings.Join(installed, ", "),
		"RootLabel":     sshRootLabels[root],
		"RootValue":     root,
		"HasKey":        hasKey,
		"PasswordsOn":   eff["passwordauthentication"] == "yes",
		"Port":          eff["port"],
		"Tries":         tries,
		"X11On":         eff["x11forwarding"] == "yes",
		"CanManage":     ok,
		"CanManageKeys": ok && user.RoleName == "admin",
		"AgentError":    agentMsg,
	}
	if agentMsg == "" {
		port := eff["port"]
		if port == "" {
			port = "22"
		}
		data["SSH"] = &sshPageStatus{
			Port:   eff["port"],
			FormPort: port,
			Root:   root,
			Managed: st.Managed,
		}
	}
	data["Pending"] = s.securityPending(r.Context())
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_ssh", data)
}

// sshPageStatus is the page's status bar / form values.
type sshPageStatus struct {
	Port     string
	FormPort string
	Root     string
	Managed  string
}

// sshAdminGate is SecurityService::administrator (the key endpoints).
func sshAdminGate(user *identity.User) error {
	if user.RoleName != "admin" {
		return errors.New("Only administrators manage the server's SSH keys.")
	}
	return nil
}

// sshConfigure hardens the SSH server (SecurityController::configureSsh →
// SecurityService::configureSsh, the guarded ssh.configure).
func (s *Server) sshConfigure(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	intOr := func(v string, def int) int {
		if v == "" {
			return def
		}
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		return 0
	}
	set := map[string]any{
		"port":                    intOr(r.FormValue("port"), 22),
		"permit_root_login":       orDefault(r.FormValue("permit_root_login"), "prohibit-password"),
		"password_authentication": r.FormValue("password_authentication") == "1",
		"max_auth_tries":          intOr(r.FormValue("max_auth_tries"), 4),
		"x11_forwarding":          r.FormValue("x11_forwarding") == "1",
	}
	reply, err := s.agent.Call(r.Context(), "ssh.configure", set, 90*time.Second)
	if err != nil {
		agentErrorRedirect(w, r, "ssh configure", err, "/security/ssh")
		return
	}
	sess.Flash("success", s.guardRecord(r, "ssh.configure", reply, set))
	http.Redirect(w, r, "/security/ssh", http.StatusSeeOther)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// sshAddKey adds an existing public key (SecurityController::addSshKey).
func (s *Server) sshAddKey(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	to := "/security/ssh"
	if err := sshAdminGate(user); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	target := r.FormValue("user")
	key := strings.TrimSpace(r.FormValue("key"))
	res, err := s.agent.Call(r.Context(), "ssh.key_add", map[string]any{"user": target, "key": key}, 60*time.Second)
	if err != nil {
		agentErrorRedirect(w, r, "ssh key add", err, to)
		return
	}
	var kr struct {
		Key struct {
			Fingerprint string `json:"fingerprint"`
			Type        string `json:"type"`
			Comment     string `json:"comment"`
		} `json:"key"`
	}
	if err := json.Unmarshal(res, &kr); err != nil {
		s.log.Error("ssh key add", "reply", err)
	}
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "ssh.key_add",
		TargetType: "server",
		TargetID:   "ssh",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"user": target, "fingerprint": kr.Key.Fingerprint, "type": kr.Key.Type, "comment": kr.Key.Comment},
	})
	sess.Flash("success", fmt.Sprintf("Key %s added for %s. Test a new SSH connection with it before turning off password logins.", kr.Key.Fingerprint, target))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// sshRemoveKey removes a key by fingerprint (SecurityController::removeSshKey).
func (s *Server) sshRemoveKey(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	to := "/security/ssh"
	if err := sshAdminGate(user); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	target := r.FormValue("user")
	fingerprint := r.FormValue("fingerprint")
	if _, err := s.agent.Call(r.Context(), "ssh.key_remove", map[string]any{"user": target, "fingerprint": fingerprint}, 60*time.Second); err != nil {
		agentErrorRedirect(w, r, "ssh key remove", err, to)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "ssh.key_remove",
		TargetType: "server",
		TargetID:   "ssh",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"user": target, "fingerprint": fingerprint},
	})
	sess.Flash("success", "Key removed from "+target+".")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// commentCleanRe is the generate form's comment cleanup.
var commentCleanRe = regexp.MustCompile(`[^A-Za-z0-9@._+-]+`)
var hostCleanRe = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// sshGenerateKey generates a key pair in the panel and allows its public
// key (SecurityController::generateSshKey). The private key is shown on the
// response only — no redirect, no session, no cache.
func (s *Server) sshGenerateKey(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	to := "/security/ssh"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	if err := sshAdminGate(user); err != nil {
		fail(err.Error())
		return
	}
	target := r.FormValue("user")
	passphrase, confirmation := r.FormValue("passphrase"), r.FormValue("passphrase_confirmation")
	if passphrase != confirmation {
		fail("The passphrases do not match.")
		return
	}
	if passphrase != "" && (utf8.RuneCountInString(passphrase) < 8 || len(passphrase) > 1024) {
		fail("Use a passphrase of at least 8 characters, or leave it empty.")
		return
	}
	comment := strings.Trim(commentCleanRe.ReplaceAllString(r.FormValue("comment"), "-"), "-")
	if comment == "" {
		comment = target + "@whacp"
	}
	if len(comment) > 64 {
		comment = comment[:64]
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		fail("The key pair could not be generated.")
		return
	}
	der, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		fail("The key pair could not be generated.")
		return
	}
	var pemBytes []byte
	if passphrase == "" {
		pemBytes = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	} else if derEnc, err := x509.PEMEncrypt("AES-256-CBC", []byte(passphrase), der, rand.Reader); err == nil {
		pemBytes = pem.EncodeToMemory(derEnc)
	}
	if len(pemBytes) == 0 {
		fail("The key pair could not be generated.")
		return
	}
	public := "ssh-rsa " + base64.StdEncoding.EncodeToString(sshRsaWire(rsaKey.PublicKey.N, rsaKey.PublicKey.E)) + " " + comment
	res, err := s.agent.Call(r.Context(), "ssh.key_add", map[string]any{"user": target, "key": public}, 60*time.Second)
	if err != nil {
		agentErrorRedirect(w, r, "ssh key generate", err, to)
		return
	}
	var kr struct {
		Key struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"key"`
	}
	if err := json.Unmarshal(res, &kr); err != nil {
		s.log.Error("ssh key generate", "reply", err)
	}
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "ssh.key_generate",
		TargetType: "server",
		TargetID:   "ssh",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"user": target, "fingerprint": kr.Key.Fingerprint, "type": "ssh-rsa", "comment": comment, "passphrase": passphrase != ""},
	})
	// The page's connection help: the effective port and the panel host.
	port := "22"
	if res, err := s.agent.Call(r.Context(), "ssh.status", nil); err == nil {
		var st struct {
			Effective map[string]string `json:"effective"`
		}
		if json.Unmarshal(res, &st) == nil && st.Effective["port"] != "" {
			port = st.Effective["port"]
		}
	}
	host := r.Host
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	serverName := "your-server"
	if hostCleanRe.MatchString(host) {
		serverName = host
	}
	file := "whacp-" + regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(target, "") + "-" + regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(serverName, "") + ".pem"
	portArg := ""
	if port != "" && port != "22" {
		portArg = " -p " + port
	}
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	s.render(w, "security_ssh_key_created", map[string]any{
		"User":         user,
		"CSRF":         session.CSRFToken(sess),
		"Private":      string(pemBytes),
		"PrivateB64":   base64.StdEncoding.EncodeToString(pemBytes),
		"Public":       public,
		"Fingerprint": kr.Key.Fingerprint,
		"Account":   target,
		"Encrypted": passphrase != "",
		"SSHPort":   port,
		"PortArg":   portArg,
		"ServerName": serverName,
		"File":      file,
	})
}

// sshRsaWire is the ssh-rsa public key wire format (string "ssh-rsa",
// mpint e, mpint n).
func sshRsaWire(n, e *big.Int) []byte {
	var out bytes.Buffer
	write := func(b []byte) {
		var lenBuf [4]byte
		binaryU32(lenBuf[:], uint32(len(b)))
		out.Write(lenBuf[:])
		out.Write(b)
	}
	write([]byte("ssh-rsa"))
	write(mpint(e))
	write(mpint(n))
	return out.Bytes()
}

func binaryU32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

// mpint is the ssh "mpint" (multi-precision integer) encoding.
func mpint(bi *big.Int) []byte {
	b := bi.Bytes() // big-endian, no leading zeros
	if b == nil {
		b = []byte{}
	}
	if len(b) > 0 && b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	var lenBuf [4]byte
	binaryU32(lenBuf[:], uint32(len(b)))
	return append(lenBuf[:], b...)
}
