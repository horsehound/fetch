package main

import (
	"strings"
	"sync"
)

// Tok is one run of text and its highlight class ("" for plain text). The
// texts of all tokens of an item, joined, are exactly the item's text, so a
// client only has to map classes to colours.
//
// Classes: kw keyword · str string · com comment · num number · fn function
// or command · typ type · var variable · key property, tag or key · attr
// option or attribute · meta prompt, decorator, marker · add / del diff lines.
type Tok [2]string

type toks []Tok

func (t *toks) add(class, text string) {
	if text == "" {
		return
	}
	if n := len(*t); n > 0 && (*t)[n-1][0] == class {
		(*t)[n-1][1] += text
		return
	}
	*t = append(*t, Tok{class, text})
}

type lexMode int

const (
	modeGeneric lexMode = iota
	modeShell
	modeJSON
	modeMarkup
	modeCSS
	modeYAML
	modeINI
	modeDiff
	modeDocker
)

type langSpec struct {
	mode     lexMode
	line     []string    // line comment starters
	block    [][2]string // block comment delimiters
	quotes   string      // single-character string delimiters
	triple   bool        // """ and ''' strings (Python)
	keywords map[string]bool
	types    map[string]bool
	nocase   bool // keywords match in any case (SQL)
	dollar   bool // $name is a variable
	preproc  bool // #include and friends
	cmdlets  bool // Verb-Noun commands and -Options (PowerShell)
}

const cFamily = `if else for while do switch case default break continue return goto try catch finally throw new delete this
	class struct enum interface extends implements public private protected static final const void null true false`

// langs holds every language the hub can name. "code" is the fallback for
// code in a language without rules of its own.
var langs = map[string]*langSpec{
	"code": {line: []string{"//", "#"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: wordSet(`if else for while return function def class import from true false null nil None`)},
	"bash":       {mode: modeShell},
	"dockerfile": {mode: modeDocker},
	"json":       {mode: modeJSON},
	"html":       {mode: modeMarkup},
	"liquid":     {mode: modeMarkup},
	"css":        {mode: modeCSS},
	"yaml":       {mode: modeYAML},
	"toml":       {mode: modeINI},
	"diff":       {mode: modeDiff},
	"python": {line: []string{"#"}, quotes: `"'`, triple: true,
		keywords: wordSet(`and as assert async await break class continue def del elif else except finally for from global if import
			in is lambda nonlocal not or pass raise return try while with yield match case True False None self`),
		types: wordSet(`int str float bool list dict set tuple bytes object print len range open type isinstance enumerate zip map filter sorted sum min max any all super`)},
	"javascript": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: "\"'`",
		keywords: wordSet(`async await break case catch class const continue debugger default delete do else export extends finally for from
			function if import in instanceof let new of return static super switch this throw try typeof var void while with yield
			true false null undefined`),
		types: wordSet(`Array Object String Number Boolean Promise Map Set Date Error JSON Math console document window`)},
	"typescript": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: "\"'`",
		keywords: wordSet(`abstract as async await break case catch class const continue declare default delete do else enum export extends
			finally for from function if implements import in instanceof interface is keyof let namespace new of private protected
			public readonly return satisfies static super switch this throw try type typeof var void while yield true false null undefined`),
		types: wordSet(`string number boolean any unknown never object Array Object String Number Boolean Promise Map Set Date Error Record Partial console`)},
	"go": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: "\"'`",
		keywords: wordSet(`break case chan const continue default defer else fallthrough for func go goto if import interface map package range
			return select struct switch type var true false nil iota`),
		types: wordSet(`bool byte error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any
			append cap close copy delete len make new panic print println recover`)},
	"rust": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"`,
		keywords: wordSet(`as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref
			return self Self static struct super trait type unsafe use where while true false`),
		types: wordSet(`bool char f32 f64 i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize str String Vec Option Result Box Some None Ok Err`)},
	"c": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`, preproc: true,
		keywords: wordSet(`auto break case const continue default do else enum extern for goto if inline register restrict return sizeof static
			struct switch typedef union volatile while NULL`),
		types: wordSet(`char double float int long short signed unsigned void size_t bool uint8_t uint16_t uint32_t uint64_t int8_t int16_t int32_t int64_t FILE`)},
	"cpp": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`, preproc: true,
		keywords: wordSet(cFamily + ` auto constexpr explicit friend inline namespace noexcept nullptr operator override template typename
			using virtual sizeof typedef union`),
		types: wordSet(`bool char double float int long short signed unsigned void size_t string vector map set unique_ptr shared_ptr std`)},
	"java": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: wordSet(cFamily + ` abstract assert import instanceof native package record super synchronized throws transient var volatile`),
		types:    wordSet(`boolean byte char double float int long short String Integer Long Double Boolean Object List Map Set ArrayList HashMap Optional System`)},
	"csharp": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: wordSet(cFamily + ` abstract as async await base foreach get in internal is namespace out override params readonly ref sealed set
			using var virtual where yield`),
		types: wordSet(`bool byte char decimal double float int long object short string uint ulong ushort Task List Dictionary Console`)},
	"swift": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"`,
		keywords: wordSet(`as associatedtype async await break case catch class continue default defer do else enum extension fallthrough
			fileprivate final for func guard if import in init inout internal is let mutating nil open private protocol public
			repeat return self Self some static struct subscript super switch throw throws try typealias var where while true false`),
		types: wordSet(`Any Bool Character Double Float Int String Array Dictionary Set Optional Void View Text Color Data URL Date Task`)},
	"kotlin": {line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`,
		keywords: wordSet(cFamily + ` as by companion data fun import in internal is object open override package sealed super suspend val var when`),
		types:    wordSet(`Any Boolean Byte Char Double Float Int Long Short String Unit List Map Set`)},
	"ruby": {line: []string{"#"}, quotes: `"'`,
		keywords: wordSet(`alias and begin break case class def defined do else elsif end ensure false for if in module next nil not or redo
			require rescue retry return self super then true undef unless until when while yield puts attr_accessor attr_reader attr_writer`)},
	"php": {line: []string{"//", "#"}, block: [][2]string{{"/*", "*/"}}, quotes: `"'`, dollar: true,
		keywords: wordSet(cFamily + ` abstract and array as echo elseif endif endforeach endwhile fn foreach function global include isset
			namespace or print require require_once trait unset use var`)},
	"powershell": {line: []string{"#"}, block: [][2]string{{"<#", "#>"}}, quotes: `"'`, dollar: true, cmdlets: true, nocase: true,
		keywords: wordSet(`begin break catch class continue do else elseif end exit filter finally for foreach function if in param process
			return switch throw trap try until while`)},
	"sql": {line: []string{"--"}, block: [][2]string{{"/*", "*/"}}, quotes: `'"`, nocase: true,
		keywords: wordSet(`add all alter and as asc begin between by case check column commit constraint create cross database default delete
			desc distinct drop else end exists foreign from full group having if in index inner insert into is join key left like limit
			not null offset on or order outer primary references returning right rollback select set table then transaction union unique
			update using values view when where with true false`),
		types: wordSet(`bigint boolean char date datetime decimal float int integer json jsonb numeric real serial smallint text time timestamp
			uuid varchar count sum avg min max coalesce now`)},
}

// Names people recognise, for the label on a code block.
var langLabels = map[string]string{
	"bash": "Shell", "code": "Code", "cpp": "C++", "csharp": "C#", "css": "CSS", "html": "HTML", "javascript": "JavaScript",
	"json": "JSON", "php": "PHP", "powershell": "PowerShell", "sql": "SQL", "toml": "TOML", "typescript": "TypeScript",
	"yaml": "YAML", "dockerfile": "Dockerfile", "c": "C", "go": "Go", "diff": "Diff",
}

func langLabel(id string) string {
	if l, ok := langLabels[id]; ok {
		return l
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// highlight splits code into coloured tokens for the given language.
func highlight(text, lang string) []Tok {
	sp := langs[lang]
	if sp == nil {
		sp = langs["code"]
	}
	var out toks
	// A fenced block keeps its fence lines, dimmed, around the highlighted body.
	if _, body, ok := fenced(strings.TrimSpace(text)); ok {
		start := strings.Index(text, body)
		if body != "" && start >= 0 {
			out.add("meta", text[:start])
			lex(body, sp, &out)
			out.add("meta", text[start+len(body):])
			return out
		}
	}
	lex(text, sp, &out)
	return out
}

func lex(s string, sp *langSpec, out *toks) {
	switch sp.mode {
	case modeShell:
		lexShell(s, out)
	case modeDocker:
		lexDocker(s, out)
	case modeJSON:
		lexJSON(s, out)
	case modeMarkup:
		lexMarkup(s, out)
	case modeCSS:
		lexCSS(s, out)
	case modeYAML:
		lexLines(s, out, yamlLine)
	case modeINI:
		lexLines(s, out, iniLine)
	case modeDiff:
		lexLines(s, out, diffLine)
	default:
		lexGeneric(s, sp, out)
	}
}

// ---- character helpers ----

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isIdent(c byte) bool { return isIdentStart(c) || isDigit(c) }
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func lineEnd(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(s)
}

// stringEnd returns the index just past the string that opens at s[i], or -1
// if it never closes (an apostrophe in a comment, say). Backticks and
// multiLine strings may span lines.
func stringEnd(s string, i int, multiLine bool) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case q:
			return j + 1
		case '\n':
			if !multiLine && q != '`' {
				return -1
			}
		}
	}
	return -1
}

func numberEnd(s string, i int) int {
	j := i
	for j < len(s) && (isDigit(s[j]) || isIdentStart(s[j]) && s[j] < 0x80 || s[j] == '.' && j+1 < len(s) && isDigit(s[j+1])) {
		j++
	}
	return j
}

func identEnd(s string, i int) int {
	j := i
	for j < len(s) && isIdent(s[j]) {
		j++
	}
	return j
}

// ---- the general lexer: comments, strings, numbers, words ----

func lexGeneric(s string, sp *langSpec, out *toks) {
	lineStart := true
	for i := 0; i < len(s); {
		c := s[i]
		rest := s[i:]

		if sp.preproc && lineStart && c == '#' {
			j := identEnd(s, i+1)
			out.add("meta", s[i:j])
			i, lineStart = j, false
			continue
		}
		if p := prefixOf(rest, sp.line); p != "" && !(p == "#" && i > 0 && !isSpace(s[i-1])) && !(p == "//" && i > 0 && s[i-1] == ':') {
			j := lineEnd(s, i)
			out.add("com", s[i:j])
			i = j
			continue
		}
		if b := blockOf(rest, sp.block); b != nil {
			j := len(s)
			if k := strings.Index(rest[len(b[0]):], b[1]); k >= 0 {
				j = i + len(b[0]) + k + len(b[1])
			}
			out.add("com", s[i:j])
			i = j
			continue
		}
		if sp.triple && (strings.HasPrefix(rest, `"""`) || strings.HasPrefix(rest, `'''`)) {
			j := len(s)
			if k := strings.Index(rest[3:], rest[:3]); k >= 0 {
				j = i + 3 + k + 3
			}
			out.add("str", s[i:j])
			i, lineStart = j, false
			continue
		}
		if strings.IndexByte(sp.quotes, c) >= 0 {
			if j := stringEnd(s, i, false); j > 0 {
				out.add("str", s[i:j])
				i, lineStart = j, false
				continue
			}
		}
		if c == '@' && i+1 < len(s) && isIdentStart(s[i+1]) && (i == 0 || !isIdent(s[i-1])) {
			j := identEnd(s, i+1)
			out.add("meta", s[i:j]) // decorators and annotations
			i, lineStart = j, false
			continue
		}
		if sp.dollar && c == '$' && i+1 < len(s) && (isIdentStart(s[i+1]) || s[i+1] == '_') {
			j := identEnd(s, i+1)
			if sp.cmdlets && j < len(s) && s[j] == ':' && j+1 < len(s) && isIdentStart(s[j+1]) {
				j = identEnd(s, j+1) // $env:PATH
			}
			out.add("var", s[i:j])
			i, lineStart = j, false
			continue
		}
		if sp.cmdlets && c == '-' && i > 0 && isSpace(s[i-1]) && i+1 < len(s) && isIdentStart(s[i+1]) {
			j := identEnd(s, i+1)
			out.add("attr", s[i:j]) // -Parameter
			i = j
			continue
		}
		if isDigit(c) && (i == 0 || !isIdent(s[i-1])) {
			j := numberEnd(s, i)
			out.add("num", s[i:j])
			i, lineStart = j, false
			continue
		}
		if isIdentStart(c) {
			j := identEnd(s, i)
			if sp.cmdlets {
				for j+1 < len(s) && s[j] == '-' && isIdentStart(s[j+1]) {
					j = identEnd(s, j+1) // Get-ChildItem
				}
			}
			word := s[i:j]
			look := word
			if sp.nocase {
				look = strings.ToLower(word)
			}
			switch {
			case sp.keywords[look]:
				out.add("kw", word)
			case sp.types[look]:
				out.add("typ", word)
			case sp.cmdlets && strings.Contains(word, "-"):
				out.add("fn", word)
			case j < len(s) && s[j] == '(' && !sp.nocase:
				out.add("fn", word)
			default:
				out.add("", word)
			}
			i, lineStart = j, false
			continue
		}
		if c == '\n' {
			lineStart = true
		} else if !isSpace(c) {
			lineStart = false
		}
		out.add("", s[i:i+1])
		i++
	}
}

func prefixOf(s string, options []string) string {
	for _, o := range options {
		if strings.HasPrefix(s, o) {
			return o
		}
	}
	return ""
}

func blockOf(s string, blocks [][2]string) *[2]string {
	for i := range blocks {
		if strings.HasPrefix(s, blocks[i][0]) {
			return &blocks[i]
		}
	}
	return nil
}

// ---- shell ----

func lexShell(s string, out *toks) {
	cmdPos := true // the next word is a command name
	for i := 0; i < len(s); {
		c := s[i]
		atWordStart := i == 0 || isSpace(s[i-1]) || strings.IndexByte(";|&(", s[i-1]) >= 0
		lineStart := i == 0 || s[i-1] == '\n'

		switch {
		case c == '\n':
			out.add("", "\n")
			i, cmdPos = i+1, true
		case isSpace(c):
			out.add("", s[i:i+1])
			i++
		case lineStart && (c == '$' || c == '>') && i+1 < len(s) && s[i+1] == ' ':
			out.add("meta", s[i:i+2]) // a copied prompt
			i += 2
		case c == '#' && atWordStart:
			j := lineEnd(s, i)
			out.add("com", s[i:j])
			i = j
		case c == '"' || c == '\'':
			j := stringEnd(s, i, true)
			if j < 0 {
				j = i + 1
				out.add("", s[i:j])
			} else {
				out.add("str", s[i:j])
			}
			i, cmdPos = j, false
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			out.add("", "$(")
			i, cmdPos = i+2, true
		case c == '$' && i+1 < len(s) && s[i+1] == '{':
			j := len(s)
			if k := strings.IndexByte(s[i:], '}'); k >= 0 {
				j = i + k + 1
			}
			out.add("var", s[i:j])
			i, cmdPos = j, false
		case c == '$' && i+1 < len(s) && (isIdent(s[i+1]) || strings.IndexByte("?@*#!$", s[i+1]) >= 0):
			j := i + 2
			if isIdentStart(s[i+1]) {
				j = identEnd(s, i+1)
			}
			out.add("var", s[i:j])
			i, cmdPos = j, false
		case c == '|' || c == '&' || c == ';' || c == '(' || c == '`':
			out.add("", s[i:i+1])
			i, cmdPos = i+1, true
		case c == '-' && atWordStart && i+1 < len(s) && (isIdent(s[i+1]) || s[i+1] == '-'):
			j := i
			for j < len(s) && !isSpace(s[j]) && s[j] != '=' && strings.IndexByte(";|&)", s[j]) < 0 {
				j++
			}
			out.add("attr", s[i:j])
			i = j
		default:
			j := i
			for j < len(s) && !isSpace(s[j]) && strings.IndexByte(";|&()\"'`$", s[j]) < 0 {
				j++
			}
			if j == i {
				j = i + 1
			}
			word := s[i:j]
			switch {
			case cmdPos && shellAssign.MatchString(word):
				eq := strings.IndexByte(word, '=')
				out.add("var", word[:eq])
				out.add("", word[eq:])
				// still in command position: FOO=bar command
			case cmdPos && (shellKeywords[word] || word == "export" || word == "local" || word == "return"):
				out.add("kw", word)
				cmdPos = word != "in" && word != "export" && word != "local" && word != "return" && word != "for" && word != "case" && word != "function"
			case cmdPos && word != "[" && word != "[[" && word != "{" && word != "}" && word != "!":
				out.add("fn", word)
				cmdPos = word == "sudo" || word == "doas" || word == "exec" || word == "time" || word == "nohup" || word == "xargs" || word == "env"
			default:
				out.add("", word)
			}
			i = j
		}
	}
}

var dockerInstr = wordSet(`FROM RUN CMD LABEL MAINTAINER EXPOSE ENV ADD COPY ENTRYPOINT VOLUME USER WORKDIR ARG ONBUILD STOPSIGNAL HEALTHCHECK SHELL AS`)

func lexDocker(s string, out *toks) {
	lexLines(s, out, func(line string, out *toks) {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		word, rest, _ := strings.Cut(trimmed, " ")
		if strings.HasPrefix(trimmed, "#") {
			out.add("com", line)
			return
		}
		if !dockerInstr[word] {
			lexShell(line, out) // a continued RUN line
			return
		}
		out.add("", indent)
		out.add("kw", word)
		if len(trimmed) == len(word) {
			return
		}
		out.add("", " ")
		if word == "RUN" {
			lexShell(rest, out)
			return
		}
		var sub toks
		lexShell(rest, &sub)
		for _, t := range sub {
			if t[0] == "fn" {
				t[0] = "" // only RUN lines contain commands
			}
			out.add(t[0], t[1])
		}
	})
}

// ---- line-oriented formats ----

func lexLines(s string, out *toks, each func(line string, out *toks)) {
	for len(s) > 0 {
		j := strings.IndexByte(s, '\n')
		if j < 0 {
			each(s, out)
			return
		}
		each(s[:j], out)
		out.add("", "\n")
		s = s[j+1:]
	}
}

func scalar(v string, out *toks) {
	t := strings.TrimSpace(v)
	lead := v[:len(v)-len(strings.TrimLeft(v, " \t"))]
	trail := v[len(lead)+len(t):]
	// Split off a trailing comment: value  # note
	comment := ""
	if !strings.HasPrefix(t, `"`) && !strings.HasPrefix(t, `'`) {
		if k := strings.Index(t, " #"); k >= 0 {
			comment, t = t[k:], t[:k]
		}
	}
	out.add("", lead)
	switch lower := strings.ToLower(t); {
	case t == "":
	case t[0] == '"' || t[0] == '\'':
		out.add("str", t)
	case lower == "true" || lower == "false" || lower == "null" || lower == "yes" || lower == "no" || lower == "~":
		out.add("kw", t)
	case isDigit(t[0]) && numberEnd(t, 0) == len(t), t[0] == '-' && len(t) > 1 && isDigit(t[1]) && numberEnd(t, 1) == len(t):
		out.add("num", t)
	default:
		out.add("", t)
	}
	out.add("com", comment)
	out.add("", trail)
}

func yamlLine(line string, out *toks) {
	trimmed := strings.TrimLeft(line, " \t")
	out.add("", line[:len(line)-len(trimmed)])
	switch {
	case strings.HasPrefix(trimmed, "#"):
		out.add("com", trimmed)
		return
	case trimmed == "---" || trimmed == "...":
		out.add("meta", trimmed)
		return
	}
	for strings.HasPrefix(trimmed, "- ") {
		out.add("", "- ")
		trimmed = trimmed[2:]
	}
	if k := strings.Index(trimmed, ":"); k > 0 && (k == len(trimmed)-1 || trimmed[k+1] == ' ') && !strings.ContainsAny(trimmed[:1], `"'[{`) {
		out.add("key", trimmed[:k])
		out.add("", ":")
		scalar(trimmed[k+1:], out)
		return
	}
	scalar(trimmed, out)
}

func iniLine(line string, out *toks) {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";"):
		out.add("com", line)
	case strings.HasPrefix(trimmed, "["):
		out.add("kw", line)
	default:
		if k := strings.Index(line, "="); k > 0 {
			out.add("key", line[:k])
			out.add("", "=")
			scalar(line[k+1:], out)
			return
		}
		out.add("", line)
	}
}

func diffLine(line string, out *toks) {
	switch {
	case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index "):
		out.add("com", line)
	case strings.HasPrefix(line, "@@"):
		out.add("meta", line)
	case strings.HasPrefix(line, "+"):
		out.add("add", line)
	case strings.HasPrefix(line, "-"):
		out.add("del", line)
	default:
		out.add("", line)
	}
}

// ---- JSON ----

func lexJSON(s string, out *toks) {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			j := stringEnd(s, i, false)
			if j < 0 {
				j = len(s)
			}
			k := j
			for k < len(s) && isSpace(s[k]) {
				k++
			}
			if k < len(s) && s[k] == ':' {
				out.add("key", s[i:j])
			} else {
				out.add("str", s[i:j])
			}
			i = j
		case isDigit(c) || c == '-' && i+1 < len(s) && isDigit(s[i+1]):
			j := numberEnd(s, i+1)
			out.add("num", s[i:j])
			i = j
		case isIdentStart(c):
			j := identEnd(s, i)
			out.add("kw", s[i:j]) // true, false, null
			i = j
		default:
			out.add("", s[i:i+1])
			i++
		}
	}
}

// ---- HTML, XML and Liquid templates ----

func lexMarkup(s string, out *toks) {
	for i := 0; i < len(s); {
		rest := s[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			j := len(s)
			if k := strings.Index(rest, "-->"); k >= 0 {
				j = i + k + 3
			}
			out.add("com", s[i:j])
			i = j
		case strings.HasPrefix(rest, "{%") || strings.HasPrefix(rest, "{{"):
			closer := "%}"
			if rest[1] == '{' {
				closer = "}}"
			}
			j := len(s)
			if k := strings.Index(rest, closer); k >= 0 {
				j = i + k + 2
			}
			lexTemplateTag(s[i:j], out)
			i = j
		case rest[0] == '<' && len(rest) > 1 && (isIdentStart(rest[1]) || rest[1] == '/' || rest[1] == '!' || rest[1] == '?'):
			i = lexTag(s, i, out)
		default:
			out.add("", s[i:i+1])
			i++
		}
	}
}

// lexTag colours one <tag attr="value"> and returns the index after it.
func lexTag(s string, i int, out *toks) int {
	j := i + 1
	for j < len(s) && (s[j] == '/' || s[j] == '!' || s[j] == '?') {
		j++
	}
	out.add("", s[i:j])
	k := j
	for k < len(s) && (isIdent(s[k]) || s[k] == '-' || s[k] == ':') {
		k++
	}
	out.add("key", s[j:k])
	for k < len(s) && s[k] != '>' {
		c := s[k]
		switch {
		case c == '"' || c == '\'':
			e := stringEnd(s, k, true)
			if e < 0 {
				e = len(s)
			}
			out.add("str", s[k:e])
			k = e
		case strings.HasPrefix(s[k:], "{{") || strings.HasPrefix(s[k:], "{%"):
			closer := "%}"
			if s[k+1] == '{' {
				closer = "}}"
			}
			e := len(s)
			if m := strings.Index(s[k:], closer); m >= 0 {
				e = k + m + 2
			}
			lexTemplateTag(s[k:e], out)
			k = e
		case isIdentStart(c) || c == '@' || c == ':':
			e := k + 1
			for e < len(s) && (isIdent(s[e]) || strings.IndexByte("-:.@", s[e]) >= 0) {
				e++
			}
			out.add("attr", s[k:e])
			k = e
		default:
			out.add("", s[k:k+1])
			k++
		}
	}
	if k < len(s) {
		out.add("", ">")
		k++
	}
	return k
}

var liquidWords = wordSet(`if elsif else endif unless endunless for endfor in case when endcase assign capture endcapture render include
	section schema endschema comment endcomment raw endraw liquid paginate endpaginate form endform style endstyle javascript
	endjavascript break continue and or contains with as echo limit offset reversed block endblock extends set endset macro endmacro`)

func lexTemplateTag(tag string, out *toks) {
	if len(tag) < 4 {
		out.add("meta", tag)
		return
	}
	open, body, closeTag := tag[:2], tag[2:len(tag)-2], tag[len(tag)-2:]
	if !strings.HasSuffix(tag, "}}") && !strings.HasSuffix(tag, "%}") {
		body, closeTag = tag[2:], ""
	}
	out.add("meta", open)
	lexGeneric(body, &langSpec{quotes: `"'`, keywords: liquidWords}, out)
	out.add("meta", closeTag)
}

// ---- CSS ----

func lexCSS(s string, out *toks) {
	depth := 0
	inValue := false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case strings.HasPrefix(s[i:], "/*"):
			j := len(s)
			if k := strings.Index(s[i+2:], "*/"); k >= 0 {
				j = i + 2 + k + 2
			}
			out.add("com", s[i:j])
			i = j
		case c == '"' || c == '\'':
			j := stringEnd(s, i, false)
			if j < 0 {
				j = i + 1
				out.add("", s[i:j])
			} else {
				out.add("str", s[i:j])
			}
			i = j
		case c == '{':
			depth++
			inValue = false
			out.add("", "{")
			i++
		case c == '}':
			if depth > 0 {
				depth--
			}
			inValue = false
			out.add("", "}")
			i++
		case c == ';':
			inValue = false
			out.add("", ";")
			i++
		case c == '@':
			j := i + 1
			for j < len(s) && (isIdent(s[j]) || s[j] == '-') {
				j++
			}
			out.add("kw", s[i:j])
			i = j
		case inValue && (isDigit(c) || c == '.' && i+1 < len(s) && isDigit(s[i+1])):
			j := i
			for j < len(s) && (isIdent(s[j]) || s[j] == '.' || s[j] == '%') {
				j++
			}
			out.add("num", s[i:j])
			i = j
		case inValue && c == '#':
			j := identEnd(s, i+1)
			out.add("num", s[i:j]) // a colour
			i = j
		case isIdentStart(c) || c == '-' && i+1 < len(s) && (isIdentStart(s[i+1]) || s[i+1] == '-'):
			j := i + 1
			for j < len(s) && (isIdent(s[j]) || s[j] == '-') {
				j++
			}
			word := s[i:j]
			k := j
			for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
				k++
			}
			switch {
			case depth > 0 && !inValue && k < len(s) && s[k] == ':' && !isNestedSelector(s, k):
				out.add("key", word)
				out.add("", s[j:k+1])
				inValue = true
				j = k + 1
			case inValue && j < len(s) && s[j] == '(':
				out.add("fn", word)
			case !inValue:
				out.add("typ", word) // part of a selector
			default:
				out.add("", word)
			}
			i = j
		default:
			out.add("", s[i:i+1])
			i++
		}
	}
}

// isNestedSelector tells "a:hover {" (a selector) from "color: red;" (a
// declaration) by which of { or ; comes first after the colon.
func isNestedSelector(s string, colon int) bool {
	for k := colon + 1; k < len(s); k++ {
		switch s[k] {
		case '{':
			return true
		case ';', '}':
			return false
		}
	}
	return false
}

// ---- analysis with a cache ----

type codeInfo struct {
	lang   string
	tokens []Tok
}

// analyser remembers the result per item: text never changes after upload,
// and every client asks for the whole feed on every change.
type analyser struct {
	mu    sync.Mutex
	cache map[string]codeInfo
}

// annotate fills in Lang and Tokens on the text items of a feed and forgets
// items that are no longer in it.
func (a *analyser) annotate(items []Item) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := make(map[string]codeInfo, len(items))
	for i := range items {
		if items[i].Kind != "text" {
			continue
		}
		info, ok := a.cache[items[i].ID]
		if !ok {
			if lang := detectLang(items[i].Text); lang != "" {
				info = codeInfo{lang: lang, tokens: highlight(items[i].Text, lang)}
			}
		}
		next[items[i].ID] = info
		if info.lang != "" {
			items[i].Lang = info.lang
			items[i].LangLabel = langLabel(info.lang)
			items[i].Tokens = info.tokens
		}
	}
	a.cache = next
}
