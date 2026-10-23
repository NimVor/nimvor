// Package security ports the panel's password rules: PasswordHasher's base
// checks (length, username, repetition) and the configurable strength policy
// (Security → Password Strength Configuration). Hashing itself lives in
// internal/identity (HashPassword), which must stay byte-compatible with
// PHP's password_hash.
package security

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
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

// Describe is PasswordPolicy::describe: one line describing the rules, for
// password forms.
func (p *Policy) Describe(ctx context.Context) (string, error) {
	enabled, err := p.settings.Bool(ctx, "policy_password_strength", true)
	if err != nil {
		return "", err
	}
	if !enabled {
		return fmt.Sprintf("At least %d characters.", MinPasswordLength), nil
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
	parts := []string{fmt.Sprintf("at least %d characters", minLen)}
	type countRule struct {
		key   string
		label string
	}
	for _, rule := range []countRule{
		{"password_min_special", "special"},
		{"password_min_numeric", "numeric"},
		{"password_min_alpha", "alphabet"},
		{"password_min_upper", "uppercase"},
		{"password_min_lower", "lowercase"},
	} {
		min, err := p.settings.Int(ctx, rule.key, 0)
		if err != nil {
			return "", err
		}
		if min > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", min, rule.label))
		}
	}
	s := strings.Join(parts, ", ")
	line := strings.ToUpper(s[:1]) + s[1:] // ucfirst
	if minStrength > 0 {
		line += fmt.Sprintf("; strength %d or more.", minStrength)
	} else {
		line += "."
	}
	return line, nil
}

// Generate is PasswordPolicy::generate: a random password that passes Check —
// at least two of each character class (more when the policy demands it),
// filled out from all classes, shuffled and re-rolled until acceptable.
// exclude removes characters (a mailbox password may not contain ':').
func (p *Policy) Generate(ctx context.Context, length int, exclude string) (string, error) {
	sets := []string{
		"abcdefghijkmnopqrstuvwxyz",
		"ABCDEFGHJKLMNPQRSTUVWXYZ",
		"23456789",
		"!#%+-.=?@^_~",
	}
	for i, s := range sets {
		sets[i] = strings.ReplaceAll(s, exclude, "")
	}
	counts := map[string]int{}
	sum := 0
	for _, key := range []string{"password_min_special", "password_min_numeric", "password_min_alpha", "password_min_upper", "password_min_lower"} {
		n, err := p.settings.Int(ctx, key, 0)
		if err != nil {
			return "", err
		}
		counts[key] = n
		sum += n
	}
	minLen, err := p.settings.Int(ctx, "password_min_length", MinPasswordLength)
	if err != nil {
		return "", err
	}
	if minLen < MinPasswordLength {
		minLen = MinPasswordLength
	}
	if sum+4 > length {
		length = sum + 4
	}
	if minLen > length {
		length = minLen
	}
	need := 2
	if counts["password_min_special"] > need {
		need = counts["password_min_special"]
	}
	if counts["password_min_numeric"] > need {
		need = counts["password_min_numeric"]
	}
	if counts["password_min_upper"] > need {
		need = counts["password_min_upper"]
	}
	if counts["password_min_lower"] > need {
		need = counts["password_min_lower"]
	}
	for try := 0; try < 100; try++ {
		c := make([]rune, 0, length)
		for _, set := range sets {
			for i := 0; i < need && i < len(set); i++ {
				r, err := randomRune(set)
				if err != nil {
					return "", err
				}
				c = append(c, r)
			}
		}
		all := sets[0] + sets[1] + sets[2] + sets[3]
		for len(c) < length {
			r, err := randomRune(all)
			if err != nil {
				return "", err
			}
			c = append(c, r)
		}
		for i := len(c) - 1; i > 0; i-- {
			j, err := randomInt(i + 1)
			if err != nil {
				return "", err
			}
			c[i], c[j] = c[j], c[i]
		}
		password := string(c)
		if weak, err := p.Check(ctx, password, ""); err != nil {
			return "", err
		} else if weak == "" {
			return password, nil
		}
	}
	return "", errors.New("could not generate a password that meets the policy")
}

// randomRune picks a random character of s.
func randomRune(s string) (rune, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(s))))
	if err != nil {
		return 0, err
	}
	return rune(s[int(n.Int64())]), nil
}

// randomInt returns a uniform random integer in [0, n).
func randomInt(n int) (int, error) {
	if n <= 1 {
		return 0, nil
	}
	x, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(x.Int64()), nil
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
