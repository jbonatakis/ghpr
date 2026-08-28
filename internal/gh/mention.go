package gh

import "strings"

// Mentions reports whether text addresses login with an @mention.
//
// The bounds matter more than the search. A GitHub login is made of letters,
// digits and hyphens, so "@jack" must not match inside "@jack-bonatakis", and
// "notify@jack.example" is an address rather than a mention. Team references
// read "@acme/reviewers", which names the team and not the organization, so a
// following slash disqualifies the match too.
func Mentions(text, login string) bool {
	if text == "" || login == "" {
		return false
	}
	hay := strings.ToLower(text)
	needle := "@" + strings.ToLower(login)

	for i := 0; i+len(needle) <= len(hay); {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			return false
		}
		at := i + j
		end := at + len(needle)
		if openBefore(hay, at) && closedAfter(hay, end) {
			return true
		}
		i = at + 1
	}
	return false
}

// openBefore reports whether the byte preceding the @ leaves it free to start
// a mention. Anything that could be part of a login means this @ is embedded
// in something else — an email address, most often.
func openBefore(s string, at int) bool {
	if at == 0 {
		return true
	}
	return !loginByte(s[at-1])
}

// closedAfter reports whether the login ends where the match does, rather than
// continuing into a longer login or a team path.
func closedAfter(s string, end int) bool {
	if end >= len(s) {
		return true
	}
	return !loginByte(s[end]) && s[end] != '/'
}

// loginByte reports whether c can appear in a GitHub login.
func loginByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return c == '-'
}
