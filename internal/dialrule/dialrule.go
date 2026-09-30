// Package dialrule restricts which numbers a user may dial, using Asterisk-style patterns.
//
// Rules are one per line:
//
//	1001         exact number
//	_1XXX        pattern: X=0-9, Z=1-9, N=2-9, [1-5] set/range, . one or more, ! zero or more
//	-_900.       leading "-" makes it a deny rule (checked before allow rules)
//	; comment    everything after ";" is ignored
//
// A number is allowed when no deny rule matches and either there are no allow rules or
// one allow rule matches. An empty rule set allows everything.
package dialrule

import (
	"fmt"
	"regexp"
	"strings"
)

type rule struct {
	deny bool
	re   *regexp.Regexp
	src  string
}

type Rules struct {
	rules    []rule
	hasAllow bool
}

// NumberRe is the set of dial strings accepted anywhere: digits, letters and * # + . _ -.
var NumberRe = regexp.MustCompile(`^[0-9A-Za-z*#+._-]{1,64}$`)

// Parse compiles a rule set; errors name the offending line.
func Parse(text string) (*Rules, error) {
	rs := &Rules{}
	for i, line := range strings.Split(text, "\n") {
		if j := strings.IndexByte(line, ';'); j >= 0 {
			line = line[:j]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		deny := false
		if strings.HasPrefix(line, "-") {
			deny = true
			line = strings.TrimSpace(line[1:])
		}
		re, err := compile(line)
		if err != nil {
			return nil, fmt.Errorf("line %d (%q): %w", i+1, line, err)
		}
		rs.rules = append(rs.rules, rule{deny: deny, re: re, src: line})
		if !deny {
			rs.hasAllow = true
		}
	}
	return rs, nil
}

func compile(p string) (*regexp.Regexp, error) {
	if p == "" {
		return nil, fmt.Errorf("empty rule")
	}
	var b strings.Builder
	b.WriteString("^")
	if !strings.HasPrefix(p, "_") {
		if !NumberRe.MatchString(p) {
			return nil, fmt.Errorf("invalid characters")
		}
		b.WriteString(regexp.QuoteMeta(p))
		b.WriteString("$")
		return regexp.Compile(b.String())
	}
	p = p[1:]
	if p == "" {
		return nil, fmt.Errorf("empty pattern")
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == 'X' || c == 'x':
			b.WriteString("[0-9]")
		case c == 'Z' || c == 'z':
			b.WriteString("[1-9]")
		case c == 'N' || c == 'n':
			b.WriteString("[2-9]")
		case c == '.':
			if i != len(p)-1 {
				return nil, fmt.Errorf("'.' is only allowed at the end")
			}
			b.WriteString(".+")
		case c == '!':
			if i != len(p)-1 {
				return nil, fmt.Errorf("'!' is only allowed at the end")
			}
			b.WriteString(".*")
		case c == '[':
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed '['")
			}
			set := p[i+1 : i+end]
			if set == "" {
				return nil, fmt.Errorf("empty set")
			}
			b.WriteString("[")
			for j := 0; j < len(set); j++ {
				s := set[j]
				switch {
				case s >= '0' && s <= '9', s == '*', s == '#', s == '+':
					b.WriteString(regexp.QuoteMeta(string(s)))
				case s == '-' && j > 0 && j < len(set)-1 && set[j-1] <= set[j+1]:
					b.WriteByte('-')
				default:
					return nil, fmt.Errorf("invalid character %q in set", s)
				}
			}
			b.WriteString("]")
			i += end
		case c >= '0' && c <= '9', c == '*', c == '#', c == '+':
			b.WriteString(regexp.QuoteMeta(string(c)))
		default:
			return nil, fmt.Errorf("invalid character %q", c)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// Allowed reports whether number may be dialed.
func (rs *Rules) Allowed(number string) bool {
	if rs == nil {
		return true
	}
	for _, r := range rs.rules {
		if r.deny && r.re.MatchString(number) {
			return false
		}
	}
	if !rs.hasAllow {
		return true
	}
	for _, r := range rs.rules {
		if !r.deny && r.re.MatchString(number) {
			return true
		}
	}
	return false
}
