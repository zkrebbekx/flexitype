package postgres

import (
	"fmt"
	"regexp"
	"strings"
)

// extensionName bounds what requires-extension accepts. The name reaches a
// catalogue query as a parameter rather than as text, so this is not an
// injection guard — it refuses a directive line that carries anything other
// than one bare name, which would otherwise be read as an extension nobody
// has and skip the file for ever.
var extensionName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// directivePrefix marks a runner instruction in a migration file. Directives
// must appear before the first statement.
const directivePrefix = "-- +flexitype:"

// migrationDirectives are the per-file instructions the runner understands.
type migrationDirectives struct {
	// NoTransaction runs the file's statements one at a time, outside any
	// transaction. Required for statements Postgres forbids inside a
	// transaction block — CREATE INDEX CONCURRENTLY, DROP INDEX CONCURRENTLY,
	// ALTER TYPE ... ADD VALUE.
	//
	// The trade is atomicity: a failure part-way leaves earlier statements of
	// that file applied and the file unrecorded, so the next run replays it.
	// Every statement in such a file must therefore be idempotent.
	NoTransaction bool

	// RequiresExtension names a Postgres extension the file cannot run
	// without. The runner SKIPS the file when the extension is absent, and
	// does NOT record it — so a database that gains the extension later
	// applies the file on its next boot rather than never.
	//
	// It exists for one shape: an index whose operator class comes from an
	// optional extension. Such a build cannot be made conditional in SQL and
	// concurrent at the same time, because the only conditional form is a DO
	// block, a DO block is a transaction, and CREATE INDEX CONCURRENTLY
	// refuses to run inside one. Migrations 000004 and 000021 resolved that by
	// building the pg_trgm indexes plainly inside a DO block — on the largest
	// table in the database, taking a lock that conflicts with every write for
	// the whole build. Moving the condition up here lets the statement itself
	// be an ordinary concurrent build.
	//
	// Empty means the file has no extension requirement.
	RequiresExtension string
}

// parseDirectives reads the directive header of a migration file. Unknown
// directives are an error rather than a silent no-op: a typo in a directive
// that gates locking behaviour must not degrade quietly into the blocking
// default.
func parseDirectives(name, body string) (migrationDirectives, error) {
	var d migrationDirectives
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, directivePrefix) {
			continue
		}
		directive := strings.TrimSpace(strings.TrimPrefix(line, directivePrefix))
		if extension, ok := strings.CutPrefix(directive, "requires-extension"); ok {
			extension = strings.TrimSpace(extension)
			if !extensionName.MatchString(extension) {
				return d, fmt.Errorf(
					"migration %s: requires-extension needs one plain extension name, got %q", name, extension)
			}
			d.RequiresExtension = extension
			continue
		}
		switch directive {
		case "no-transaction":
			d.NoTransaction = true
		default:
			return d, fmt.Errorf("migration %s: unknown directive %q", name, line)
		}
	}
	return d, nil
}

// splitStatements splits a migration body into individual statements on
// top-level semicolons.
//
// It is only used for no-transaction files, where each statement must be sent
// separately: Postgres runs a multi-statement simple query inside an implicit
// transaction block, which is exactly what CREATE INDEX CONCURRENTLY refuses.
//
// It understands the three places a semicolon is not a separator: single-quoted
// literals, dollar-quoted bodies (including tagged ones such as $fn$), and
// line comments.
func splitStatements(body string) []string {
	var (
		out       []string
		cur       strings.Builder
		inSingle  bool
		inComment bool
		dollarTag string // non-empty while inside a dollar-quoted body
	)
	runes := []rune(body)
	for i := 0; i < len(runes); i++ {
		c := runes[i]

		if inComment {
			cur.WriteRune(c)
			if c == '\n' {
				inComment = false
			}
			continue
		}
		if dollarTag != "" {
			if tag, ok := dollarQuoteAt(runes, i); ok && tag == dollarTag {
				cur.WriteString(tag)
				i += len([]rune(tag)) - 1
				dollarTag = ""
				continue
			}
			cur.WriteRune(c)
			continue
		}
		if inSingle {
			cur.WriteRune(c)
			if c == '\'' {
				inSingle = false
			}
			continue
		}

		switch {
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			inComment = true
			cur.WriteRune(c)
		case c == '\'':
			inSingle = true
			cur.WriteRune(c)
		case c == ';':
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		default:
			if tag, ok := dollarQuoteAt(runes, i); ok {
				dollarTag = tag
				cur.WriteString(tag)
				i += len([]rune(tag)) - 1
				continue
			}
			cur.WriteRune(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// dollarQuoteAt reports whether a dollar-quote delimiter starts at i, and
// returns it. A delimiter is $$ or $tag$ where tag is a run of letters, digits
// or underscores that does not start with a digit.
func dollarQuoteAt(runes []rune, i int) (string, bool) {
	if runes[i] != '$' {
		return "", false
	}
	for j := i + 1; j < len(runes); j++ {
		c := runes[j]
		switch {
		case c == '$':
			return string(runes[i : j+1]), true
		case c >= '0' && c <= '9':
			if j == i+1 {
				return "", false // a tag must not start with a digit
			}
		case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		default:
			return "", false
		}
	}
	return "", false
}

// statementIsEmpty reports whether a split statement carries no SQL — only
// comments and whitespace. Sending one is harmless but produces a confusing
// "empty query" in logs, so the runner skips it.
func statementIsEmpty(stmt string) bool {
	for _, line := range strings.Split(stmt, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			return false
		}
	}
	return true
}
