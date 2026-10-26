package settings

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var urlRE = regexp.MustCompile(`(?i)^(https://|http://(localhost|127\.0\.0\.1)(:\d+)?(/|$))`)
var badURLRE = regexp.MustCompile(`[\s"'<>]`)

func isBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// Validate checks the submitted values of known settings the way
// SettingsService::update does. Unknown keys and keys that were not submitted
// are ignored. It returns the cleaned typed values and the error text per key.
func Validate(input map[string]string) (map[string]any, map[string]string) {
	clean := map[string]any{}
	errs := map[string]string{}
	for i := range Definitions {
		d := &Definitions[i]
		raw, ok := input[d.Key]
		if !ok {
			continue
		}
		switch {
		case d.Type == "bool":
			clean[d.Key] = isBool(raw)
		case d.Type == "int":
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || (d.HasMin && n < d.Min) || (d.HasMax && n > d.Max) {
				errs[d.Key] = fmt.Sprintf("Enter a whole number between %d and %d.", d.Min, d.Max)
				continue
			}
			clean[d.Key] = n
		case d.Type == "url":
			v := strings.TrimRight(strings.TrimSpace(raw), "/")
			max := d.MaxLength
			if max == 0 {
				max = 255
			}
			if u, err := url.Parse(v); v != "" && (err != nil || u.Host == "" || utf8.RuneCountInString(v) > max || !urlRE.MatchString(v) || badURLRE.MatchString(v)) {
				errs[d.Key] = "Enter an https:// address, or leave it empty."
				continue
			}
			clean[d.Key] = v
		case len(d.Options) > 0:
			found := false
			for _, o := range d.Options {
				found = found || o.Value == raw
			}
			if !found {
				errs[d.Key] = "Choose one of the options."
				continue
			}
			clean[d.Key] = raw
		default:
			v := strings.TrimSpace(raw)
			max := d.MaxLength
			if max == 0 {
				max = 255
			}
			printable := true
			for _, r := range v {
				if r < 0x20 || r == 0x7f {
					printable = false
				}
			}
			if (v == "" && !d.Optional) || utf8.RuneCountInString(v) > max || !printable {
				errs[d.Key] = fmt.Sprintf("Enter between 1 and %d printable characters.", max)
				continue
			}
			if v != "" && d.Pattern != "" && !regexp.MustCompile(d.Pattern).MatchString(v) {
				errs[d.Key] = "This value is not valid here."
				continue
			}
			clean[d.Key] = v
		}
	}
	return clean, errs
}

// Values returns every defined setting as a display string: the stored value,
// or the default (SettingsService::all). Booleans read "1" or "0".
func (r *Repository) Values(ctx context.Context) (map[string]string, error) {
	stored, err := r.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(Definitions))
	for i := range Definitions {
		d := &Definitions[i]
		if v, ok := stored[d.Key]; ok {
			if d.Type == "bool" {
				if isBool(v) {
					v = "1"
				} else {
					v = "0"
				}
			}
			out[d.Key] = v
			continue
		}
		switch x := d.Default.(type) {
		case bool:
			if x {
				out[d.Key] = "1"
			} else {
				out[d.Key] = "0"
			}
		case int:
			out[d.Key] = strconv.Itoa(x)
		default:
			out[d.Key] = fmt.Sprint(x)
		}
	}
	return out, nil
}

// Save validates and stores the submitted settings in one go, recording
// nothing when a value is invalid. It returns the keys that changed (with the
// old and new value) or the per-key errors.
func (r *Repository) Save(ctx context.Context, input map[string]string, actor int64) (changed map[string][2]string, errs map[string]string, err error) {
	clean, errs := Validate(input)
	if len(errs) > 0 {
		return nil, errs, nil
	}
	before, err := r.Values(ctx)
	if err != nil {
		return nil, nil, err
	}
	changed = map[string][2]string{}
	for k, v := range clean {
		s := fmt.Sprint(v)
		if b, ok := v.(bool); ok {
			s = "0"
			if b {
				s = "1"
			}
		}
		if before[k] == s {
			continue
		}
		if err := r.Set(ctx, k, v, actor); err != nil {
			return nil, nil, err
		}
		changed[k] = [2]string{before[k], s}
	}
	return changed, nil, nil
}
