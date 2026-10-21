package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// AccountOptions is the hosting option set of an account (AccountOptions):
// how DNS records and mail are provisioned for the account's new websites.
type AccountOptions struct {
	DNSDKIM        bool   `json:"dns_dkim"`
	DNSSPF         bool   `json:"dns_spf"`
	DNSDMARC       bool   `json:"dns_dmarc"`
	DNSRegistrarNS bool   `json:"dns_registrar_ns"`
	DNSOverwrite   bool   `json:"dns_overwrite"`
	MailRouting    string `json:"mail_routing"` // auto, local, backup or remote
	SpamFilter     bool   `json:"spam_filter"`
	SpamBox        bool   `json:"spam_box"`
}

// defaultAccountOptions mirrors AccountOptions::DEFAULTS.
func defaultAccountOptions() AccountOptions {
	return AccountOptions{
		DNSDKIM:     true,
		DNSSPF:      true,
		DNSDMARC:    true,
		MailRouting: "auto",
		SpamFilter:  true,
		SpamBox:     true,
	}
}

// mailRoutings are the accepted mail_routing values (AccountOptions::set).
var mailRoutings = map[string]bool{"auto": true, "local": true, "backup": true, "remote": true}

// storedOptions mirrors the JSON blob with pointers so "stored but missing"
// is distinguishable from a zero value.
type storedOptions struct {
	DNSDKIM        *bool   `json:"dns_dkim"`
	DNSSPF         *bool   `json:"dns_spf"`
	DNSDMARC       *bool   `json:"dns_dmarc"`
	DNSRegistrarNS *bool   `json:"dns_registrar_ns"`
	DNSOverwrite   *bool   `json:"dns_overwrite"`
	MailRouting    *string `json:"mail_routing"`
	SpamFilter     *bool   `json:"spam_filter"`
	SpamBox        *bool   `json:"spam_box"`
}

// AccountOptionStore reads and writes account hosting options
// (AccountOptions).
type AccountOptionStore struct{ db *sql.DB }

// NewAccountOptionStore wraps a database handle.
func NewAccountOptionStore(db *sql.DB) *AccountOptionStore { return &AccountOptionStore{db: db} }

// Get returns the account's options, or the defaults for a nil id or a
// missing row (AccountOptions::get). Unknown stored keys are dropped.
func (s *AccountOptionStore) Get(ctx context.Context, userID *int64) (AccountOptions, error) {
	out := defaultAccountOptions()
	if userID == nil {
		return out, nil
	}
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT options FROM account_options WHERE user_id = ?", *userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var stored storedOptions
	if raw.Valid {
		_ = json.Unmarshal([]byte(raw.String), &stored)
	}
	if stored.DNSDKIM != nil {
		out.DNSDKIM = *stored.DNSDKIM
	}
	if stored.DNSSPF != nil {
		out.DNSSPF = *stored.DNSSPF
	}
	if stored.DNSDMARC != nil {
		out.DNSDMARC = *stored.DNSDMARC
	}
	if stored.DNSRegistrarNS != nil {
		out.DNSRegistrarNS = *stored.DNSRegistrarNS
	}
	if stored.DNSOverwrite != nil {
		out.DNSOverwrite = *stored.DNSOverwrite
	}
	if stored.MailRouting != nil {
		out.MailRouting = *stored.MailRouting
	}
	if stored.SpamFilter != nil {
		out.SpamFilter = *stored.SpamFilter
	}
	if stored.SpamBox != nil {
		out.SpamBox = *stored.SpamBox
	}
	return out, nil
}

// Set stores known keys from in on top of the account's current options
// (AccountOptions::set): booleans are validated, mail_routing falls back to
// "auto", unknown keys are dropped, missing keys keep their value.
func (s *AccountOptionStore) Set(ctx context.Context, userID int64, in map[string]any) error {
	merged, err := s.Get(ctx, &userID)
	if err != nil {
		return err
	}
	for k, v := range in {
		switch k {
		case "dns_dkim":
			merged.DNSDKIM = asBool(v)
		case "dns_spf":
			merged.DNSSPF = asBool(v)
		case "dns_dmarc":
			merged.DNSDMARC = asBool(v)
		case "dns_registrar_ns":
			merged.DNSRegistrarNS = asBool(v)
		case "dns_overwrite":
			merged.DNSOverwrite = asBool(v)
		case "mail_routing":
			if str, ok := v.(string); ok && mailRoutings[str] {
				merged.MailRouting = str
			}
		case "spam_filter":
			merged.SpamFilter = asBool(v)
		case "spam_box":
			merged.SpamBox = asBool(v)
		}
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO account_options (user_id, options, updated_at) VALUES (?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET options = excluded.options, updated_at = excluded.updated_at`,
		userID, raw, now)
	return err
}

// asBool mimics filter_var($v, FILTER_VALIDATE_BOOL) for the option values a
// form can send ("1", "on", …).
func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return formBool(t)
	default:
		return false
	}
}
