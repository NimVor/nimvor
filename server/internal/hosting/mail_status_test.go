package hosting

import (
	"encoding/json"
	"testing"
)

// The agent's mail.status reports installed as a per-package map and the spam
// scores as add_header / reject; both used to fail to load.
func TestMailStatusParsesTheAgentReply(t *testing.T) {
	var st MailStatus
	body := `{"installed":{"postfix":true,"dovecot":true,"opendkim":false},"configured":true,"hostname":"mail.example.com",
	"running":{"postfix":true,"dovecot":true,"opendkim":true},
	"spam":{"enabled":true,"add_header":7.5,"reject":20},"spam_installed":true,"spam_running":true,
	"webmail":{"installed":true,"enabled":true,"hostname":"webmail.example.com","php":"8.3","running":true}}`
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st.AllInstalled() {
		t.Error("opendkim is missing: AllInstalled must be false")
	}
	if st.Spam.AddHeader == nil || *st.Spam.AddHeader != 7.5 || *st.Spam.Reject != 20 {
		t.Errorf("spam scores not loaded: %+v", st.Spam)
	}
	if !st.SpamInstalled || !st.Webmail.Installed || st.Webmail.PHP != "8.3" || !st.Webmail.Running {
		t.Errorf("spam/webmail state not loaded: %+v", st)
	}
	var old MailStatus
	if err := json.Unmarshal([]byte(`{"installed":true}`), &old); err != nil || !old.AllInstalled() {
		t.Errorf("a bare installed:true must still read as installed (err %v)", err)
	}
}
