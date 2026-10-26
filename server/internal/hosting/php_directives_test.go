package hosting

import "testing"

func TestValidatePhpINI(t *testing.T) {
	root := "/var/www/example.com"
	ok, errs := ValidatePhpINI(map[string]string{
		"display_errors": "off", "post_max_size": "256m", "date.timezone": "Europe/London", "max_input_vars": "5000", "upload_max_filesize": "", "error_log": root + "/logs/php.log",
	}, root)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if ok["display_errors"] != "Off" || ok["post_max_size"] != "256M" || ok["max_input_vars"] != "5000" {
		t.Errorf("values not normalised: %v", ok)
	}
	if _, set := ok["upload_max_filesize"]; set {
		t.Error("a blank directive must stay on the server default")
	}
	_, errs = ValidatePhpINI(map[string]string{
		"display_errors": "maybe", "post_max_size": "lots", "error_log": "/etc/passwd", "date.timezone": "Mars/Base", "allow_url_include": "On", "max_input_vars": "99999999",
	}, root)
	for _, k := range []string{"ini.display_errors", "ini.post_max_size", "ini.error_log", "ini.date.timezone", "ini", "ini.max_input_vars"} {
		if errs[k] == "" {
			t.Errorf("expected an error for %s, got %v", k, errs)
		}
	}
}
