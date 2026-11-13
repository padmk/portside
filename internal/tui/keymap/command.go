package keymap

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// ParseCommand splits a command line into the command and its arguments;
// double quotes keep spaces in one argument, and a leading colon is
// optional. "q" is quit.
func ParseCommand(line string) (string, []string, error) {
	var words []string
	for _, t := range tokens([]rune(line)) {
		if t.open {
			return "", nil, fmt.Errorf("unterminated quote")
		}
		words = append(words, t.text)
	}
	if len(words) == 0 {
		return "", nil, fmt.Errorf("empty command")
	}
	if words[0] == "q" {
		words[0] = "quit"
	}
	return words[0], words[1:], nil
}

// token is one word of a command line: its text without quotes and the
// runes it spans, [start, end); open is set when its quote is not closed.
type token struct {
	text       string
	start, end int
	open       bool
}

// tokens splits line into words, skipping one leading colon.
func tokens(line []rune) []token {
	var out []token
	var b strings.Builder
	cur := token{start: -1}
	quoted := false
	flush := func(end int) {
		if cur.start >= 0 {
			cur.text, cur.end, cur.open = b.String(), end, quoted
			out = append(out, cur)
		}
		b.Reset()
		cur = token{start: -1}
	}
	i := 0
	for i < len(line) && unicode.IsSpace(line[i]) {
		i++
	}
	if i < len(line) && line[i] == ':' {
		i++
	}
	for ; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"':
			if cur.start < 0 {
				cur.start = i
			}
			quoted = !quoted
		case unicode.IsSpace(c) && !quoted:
			flush(i)
		default:
			if cur.start < 0 {
				cur.start = i
			}
			b.WriteRune(c)
		}
	}
	flush(len(line))
	return out
}

// Quote returns s as one command-line word: quoted when it holds a space
// or a quote, or is empty.
func Quote(s string) string {
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '"' }) {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, "") + `"`
}

func distance(a, b string) int {
	x, y := []rune(a), []rune(b)
	row := make([]int, len(y)+1)
	for j := range row {
		row[j] = j
	}
	for i, c := range x {
		prev := row[0]
		row[0] = i + 1
		for j, d := range y {
			old := row[j+1]
			cost := 0
			if c != d {
				cost = 1
			}
			row[j+1] = min(row[j+1]+1, row[j]+1, prev+cost)
			prev = old
		}
	}
	return row[len(y)]
}

// Closest returns the choice nearest to word within a few edits, or "".
func Closest(word string, choices []string) string {
	closest, best := "", 4
	for _, c := range choices {
		if d := distance(word, c); d < best {
			best, closest = d, c
		}
	}
	return closest
}

// Commands returns every command name once, sorted.
func (r *Registry) Commands() []string {
	var out []string
	for _, a := range r.actions {
		if !slices.Contains(out, a.ID) {
			out = append(out, a.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Command resolves a command name: the exact id, else the only id it
// begins, else the shortest of the ids it begins when that one begins all
// of them ("rest" is restart, not restart-group).
func (r *Registry) Command(id string) (Action, error) {
	if a, ok := r.ByID(id); ok {
		return a, nil
	}
	var matches []Action
	for _, name := range r.Commands() {
		if strings.HasPrefix(name, id) {
			a, _ := r.ByID(name)
			matches = append(matches, a)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		short := matches[0]
		for _, a := range matches[1:] {
			if len(a.ID) < len(short.ID) {
				short = a
			}
		}
		ids := make([]string, 0, len(matches))
		prefixOfAll := true
		for _, a := range matches {
			ids = append(ids, a.ID)
			prefixOfAll = prefixOfAll && strings.HasPrefix(a.ID, short.ID)
		}
		if prefixOfAll {
			return short, nil
		}
		return Action{}, fmt.Errorf("ambiguous command %q: %s", id, strings.Join(ids, ", "))
	}
	if c := Closest(id, r.Commands()); c != "" {
		return Action{}, fmt.Errorf("unknown command %q; did you mean %q?", id, c)
	}
	return Action{}, fmt.Errorf("unknown command %q; ctrl+p finds actions", id)
}

// Names are the names the command line's arguments take.
type Names struct {
	Servers, Groups, Containers, Projects, Themes, Icons, States, Computers []string
}

// For returns the names an argument kind takes.
func (n Names) For(a Arg) []string {
	switch a {
	case ArgServer:
		return n.Servers
	case ArgGroup:
		return n.Groups
	case ArgServerOrGroup:
		return append(slices.Clone(n.Servers), n.Groups...)
	case ArgContainer:
		return n.Containers
	case ArgProject:
		return n.Projects
	case ArgTheme:
		return n.Themes
	case ArgIcons:
		return n.Icons
	case ArgState:
		return n.States
	case ArgComputer:
		return n.Computers
	case ArgNone, ArgPort, ArgText:
	}
	return nil
}

// Completion is what can replace the word under the cursor: the runes
// [Start, End) of the line, with one of Items (quoted when needed). Word
// is that word's index: 0 for the command.
type Completion struct {
	Start, End int
	Word       int
	Items      []string
}

// completionLimit caps the candidates offered at once.
const completionLimit = 50

// Complete returns the completions of the word at cursor, a rune index
// into line: a command name for the first word, else a name of the kind
// the command's argument takes; names that begin with the typed part come
// first, then those that contain it.
func (r *Registry) Complete(line string, cursor int, n Names) Completion {
	runes := []rune(line)
	cursor = max(0, min(cursor, len(runes)))
	toks := tokens(runes)
	word, at := len(toks), -1
	for i, t := range toks {
		if cursor >= t.start && cursor <= t.end {
			word, at = i, i
			break
		}
		if t.start > cursor {
			word = i
			break
		}
	}
	c := Completion{Start: cursor, End: cursor, Word: word}
	typed := ""
	if at >= 0 {
		t := toks[at]
		c.Start, c.End = t.start, t.end
		// The typed part is the word up to the cursor, without quotes.
		typed = strings.ReplaceAll(string(runes[t.start:cursor]), `"`, "")
	}
	var choices []string
	switch word {
	case 0:
		for _, id := range r.Commands() {
			if a, _ := r.ByID(id); a.Group != "Navigation" {
				choices = append(choices, id)
			}
		}
	case 1:
		a, err := r.Command(toks[0].text)
		if err != nil {
			return c
		}
		// Every variant's names: :restart takes a server, a group or a
		// container.
		for _, v := range r.Variants(a.ID) {
			for _, name := range n.For(v.Arg) {
				if !slices.Contains(choices, name) {
					choices = append(choices, name)
				}
			}
		}
		if a.Arg == ArgState {
			choices = append(choices, "all")
		}
	default:
		return c
	}
	var head, rest []string
	for _, v := range choices {
		switch {
		case strings.HasPrefix(v, typed):
			head = append(head, Quote(v))
		case typed != "" && strings.Contains(v, typed):
			rest = append(rest, Quote(v))
		}
	}
	c.Items = append(head, rest...)
	if len(c.Items) > completionLimit {
		c.Items = c.Items[:completionLimit]
	}
	return c
}

// FuzzyMatch is how a query matches a text: Rank is the kind of match,
// Score orders matches of one kind and At holds the matched rune indexes
// of the text, ascending, for highlighting.
type FuzzyMatch struct {
	Rank, Score int
	At          []int
}

// The kinds of match, best first: the query's first word is the text's
// first word ("restart" for Restart web), begins it ("res"), begins a
// later word of it ("web"), or only appears in it as a subsequence ("rw").
const (
	RankLoose = iota
	RankWordStart
	RankPrefix
	RankExact
)

// Fuzzy matches query against text, ignoring case: every word of query
// must appear in text as a subsequence, else ok is false. Rank comes from
// the query's first word and the text's words (FuzzyMatch). Score adds,
// per query word, its matched runes with bonuses for runs and word starts
// and more for a word found whole; it loses two points for every
// space-separated word of text that no match touches, so of two equally
// matched texts the one the query explains better wins ("res" ranks
// Restart web above Restart the daemon in place).
func Fuzzy(query, text string) (m FuzzyMatch, ok bool) {
	t := []rune(text)
	for i, r := range t {
		t[i] = unicode.ToLower(r)
	}
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return m, false
	}
	hit := map[int]bool{}
	for _, w := range words {
		s, at, ok := subsequence([]rune(w), t)
		if !ok {
			return FuzzyMatch{}, false
		}
		// A word typed whole beats the same letters spread over a long
		// name: "restart" ranks Restart above Recreate ... tarot-tests.
		if whole := wholeWord([]rune(w), t); whole >= 0 {
			s += 2 * len([]rune(w))
			at = at[:0]
			for i := range len([]rune(w)) {
				at = append(at, whole+i)
			}
		}
		m.Score += s
		for _, i := range at {
			hit[i] = true
		}
	}
	for i := range t {
		if hit[i] {
			m.At = append(m.At, i)
		}
	}
	m.Rank = rank(words[0], t)
	// Words of text that no match touches.
	in, touched := false, false
	for i, r := range t {
		switch {
		case unicode.IsSpace(r):
			if in && !touched {
				m.Score -= 2
			}
			in, touched = false, false
		default:
			in = true
			touched = touched || hit[i]
		}
	}
	if in && !touched {
		m.Score -= 2
	}
	return m, true
}

// rank is the kind of match of the query's first word w on the lowered
// text t, whose words are its runs of letters and digits.
func rank(w string, t []rune) int {
	best, first := RankLoose, true
	for i := 0; i < len(t); {
		if !isWordRune(t[i]) {
			i++
			continue
		}
		j := i
		for j < len(t) && isWordRune(t[j]) {
			j++
		}
		word, rest := string(t[i:j]), string(t[i:])
		switch {
		case first && word == w:
			return RankExact
		case first && strings.HasPrefix(word, w):
			best = RankPrefix
		case strings.HasPrefix(rest, w):
			best = max(best, RankWordStart)
		}
		first, i = false, j
	}
	return best
}

// wholeWord is where w occurs in t as one run, at a word start when it
// does anywhere, else first; -1 when it does not occur.
func wholeWord(w, t []rune) int {
	first := -1
	for i := 0; i+len(w) <= len(t); i++ {
		if string(t[i:i+len(w)]) != string(w) {
			continue
		}
		if i == 0 || !isWordRune(t[i-1]) {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return first
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// subsequence scores the best left-to-right match of q in t, trying each
// place where its first rune occurs, and returns the runes it matched.
func subsequence(q, t []rune) (int, []int, bool) {
	best, found := 0, false
	var bestAt []int
	at := make([]int, 0, len(q))
	for start := range t {
		if t[start] != q[0] {
			continue
		}
		score, j, last := 0, 0, -2
		at = at[:0]
		for i := start; i < len(t) && j < len(q); i++ {
			if t[i] != q[j] {
				continue
			}
			score++
			if i == last+1 {
				score += 3
			}
			if i == 0 || !isWordRune(t[i-1]) {
				score += 5
			}
			at = append(at, i)
			last = i
			j++
		}
		if j == len(q) {
			score -= start / 4
			if !found || score > best {
				best, found = score, true
				bestAt = append(bestAt[:0], at...)
			}
		}
	}
	return best, bestAt, found
}
