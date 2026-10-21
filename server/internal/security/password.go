// Package security ports the panel's password rules: PasswordHasher's base
// checks (length, username, repetition) and the configurable strength policy
// (Security → Password Strength Configuration). Hashing itself lives in
// internal/identity (HashPassword), which must stay byte-compatible with
// PHP's password_hash.
package security

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/mchkhawarshad/whacp/server/internal/settings"
)

// Base length bounds (PasswordHasher::MIN_LENGTH / MAX_LENGTH).
const (
	MinPasswordLength = 12
	MaxPasswordLength = 1024
)

// ASCII-only character classes, matching the panel's PCRE patterns
// ([a-z], [A-Z], [0-9] and [^A-Za-z0-9] with /u): a Unicode letter such as
// "é" counts as a special character, not a lower-case one.
var (
	isASCIILower = func(r rune) bool { return r >= 'a' && r <= 'z' }
	isASCIIUpper = func(r rune) bool { return r >= 'A' && r <= 'Z' }
	isASCIIDigit = func(r rune) bool { return r >= '0' && r <= '9' }
	isSpecial    = func(r rune) bool { return !isASCIILower(r) && !isASCIIUpper(r) && !isASCIIDigit(r) }
)

// Weakness is the base rule of PasswordHasher::weakness: it returns a
// user-facing problem with the password, or "" when acceptable.
func Weakness(password, username string) string {
	ln := utf8.RuneCountInString(password)
	if ln < MinPasswordLength {
		return fmt.Sprintf("The password must be at least %d characters long.", MinPasswordLength)
	}
	if ln > MaxPasswordLength {
		return "The password is too long."
	}
	if username != "" && strings.Contains(strings.ToLower(password), strings.ToLower(username)) {
		return "The password must not contain the username."
	}
	if uniqueRunes(password) < 5 {
		return "The password is too repetitive."
	}
	return ""
}

// Strength is a 0–100 score (PasswordPolicy::strength): roughly the
// password's entropy in bits, from its length (repetition counts less) and
// the kinds of characters used.
func Strength(password string) int {
	runes := []rune(password)
	if len(runes) == 0 {
		return 0
	}
	pool := 0
	if containsClass(runes, isASCIILower) {
		pool += 26
	}
	if containsClass(runes, isASCIIUpper) {
		pool += 26
	}
	if containsClass(runes, isASCIIDigit) {
		pool += 10
	}
	if containsClass(runes, isSpecial) {
		pool += 33
	}
	// Repetition counts less: at most twice the number of distinct runes.
	length := len(runes)
	if l := 2 * uniqueRunes(password); l < length {
		length = l
	}
	p := float64(pool)
	if p < 2 {
		p = 2
	}
	s := float64(length) * math.Log2(p)
	if s > 100 {
		s = 100
	}
	return int(math.Floor(s))
}

// Policy applies the configured strength rules on top of the base rules.
type Policy struct {
	settings *settings.Repository
}

func NewPolicy(s *settings.Repository) *Policy { return &Policy{settings: s} }

// Check is PasswordPolicy::check: a user-facing problem with the password, or
// "" when acceptable. The base rules always apply; the configured minimum
// strength/length/character counts apply only while the policy is enabled
// (default on).
func (p *Policy) Check(ctx context.Context, password, username string) (string, error) {
	if weak := Weakness(password, username); weak != "" {
		return weak, nil
	}
	enabled, err := p.settings.Bool(ctx, "policy_password_strength", true)
	if err != nil {
		return "", err
	}
	if !enabled {
		return "", nil
	}
	minStrength, err := p.settings.Int(ctx, "password_min_strength", 65)
	if err != nil {
		return "", err
	}
	minLen, err := p.settings.Int(ctx, "password_min_length", MinPasswordLength)
	if err != nil {
		return "", err
	}
	if minLen < MinPasswordLength {
		minLen = MinPasswordLength
	}
	if utf8.RuneCountInString(password) < minLen {
		return fmt.Sprintf("The password must be at least %d characters long.", minLen), nil
	}
	// Character-count rules, in PasswordPolicy::COUNTS order.
	type countRule struct {
		key   string
		label string
		match func(r rune) bool
	}
	rules := []countRule{
		{"password_min_special", "special", isSpecial},
		{"password_min_numeric", "numeric", isASCIIDigit},
		{"password_min_alpha", "alphabet", func(r rune) bool { return isASCIILower(r) || isASCIIUpper(r) }},
		{"password_min_upper", "uppercase", isASCIIUpper},
		{"password_min_lower", "lowercase", isASCIILower},
	}
	for _, rule := range rules {
		min, err := p.settings.Int(ctx, rule.key, 0)
		if err != nil {
			return "", err
		}
		if min > 0 && countIf(password, rule.match) < min {
			return fmt.Sprintf("The password needs at least %d %s character%s.", min, rule.label, plural(min)), nil
		}
	}
	if s := Strength(password); s < minStrength {
		return fmt.Sprintf("The password is too weak (strength %d, at least %d is required). Make it longer or mix upper and lower case, digits and symbols.", s, minStrength), nil
	}
	return "", nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func uniqueRunes(s string) int {
	seen := make(map[rune]struct{}, 32)
	for _, r := range s {
		seen[r] = struct{}{}
	}
	return len(seen)
}

func containsClass(runes []rune, match func(rune) bool) bool {
	for _, r := range runes {
		if match(r) {
			return true
		}
	}
	return false
}

func countIf(s string, match func(rune) bool) int {
	n := 0
	for _, r := range s {
		if match(r) {
			n++
		}
	}
	return n
}
