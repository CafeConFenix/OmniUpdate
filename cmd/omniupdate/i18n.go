package main

import (
	"fmt"
	"strings"
)

// Bilingual strings
//
// Every user-visible string is stored as «spanish\x1fenglish». The UI
// picks the language when rendering, so switching language is instant and there
// is no need to rescan.

const langSep = "\x1f"

// T creates a bilingual text.
func T(es, en string) string { return es + langSep + en }

// pick returns the part for the requested language ("es" or "en"). A text without a marker serves both.
func pick(s, lang string) string {
	i := strings.Index(s, langSep)
	if i < 0 {
		return s
	}
	if lang == "en" {
		return s[i+len(langSep):]
	}
	return s[:i]
}

// plainES returns the text in Spanish (for internal decisions and comparisons).
func plainES(s string) string { return pick(s, "es") }

// Tf is like T but with formatting; arguments that are already bilingual (texts or errors)
// are split automatically between the two languages.
func Tf(es, en string, args ...any) string {
	build := func(format, lang string) string {
		conv := make([]any, len(args))
		for i, a := range args {
			switch v := a.(type) {
			case string:
				conv[i] = pick(v, lang)
			case error:
				conv[i] = pick(v.Error(), lang)
			default:
				conv[i] = a
			}
		}
		return fmt.Sprintf(format, conv...)
	}
	return build(es, "es") + langSep + build(en, "en")
}

// Cat concatenates bilingual or plain pieces.
func Cat(parts ...string) string {
	var es, en strings.Builder
	for _, p := range parts {
		es.WriteString(pick(p, "es"))
		en.WriteString(pick(p, "en"))
	}
	return es.String() + langSep + en.String()
}

// bilingualError is an error whose text has both languages.
type bilingualError struct{ s string }

func (e bilingualError) Error() string { return e.s }

// terr creates a bilingual error.
func terr(es, en string) error { return bilingualError{T(es, en)} }

// terrf creates a bilingual error with formatting.
func terrf(es, en string, args ...any) error { return bilingualError{Tf(es, en, args...)} }

// errText returns the (possibly bilingual) text of an error.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Join joins bilingual or plain texts with a separator (repeated in each language).
func Join(sep string, parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	out := make([]string, 0, len(parts)*2)
	for i, p := range parts {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, p)
	}
	return Cat(out...)
}
