package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Text above this size is never analysed; it is a document, not a snippet.
const maxCodeLen = 64 << 10

// detectLang decides whether a text item is code and, if so, in which
// language. It returns "" for ordinary text, a language id from langs, or
// "code" for something that is clearly code in a language it can't name.
//
// Most of what people send is prose, so the bar is deliberately high: a false
// "this is code" is more annoying than a missed one.
func detectLang(text string) string {
	t := strings.TrimSpace(text)
	if t == "" || len(text) > maxCodeLen {
		return ""
	}

	// Explicit markers win outright.
	if lang, _, ok := fenced(t); ok {
		return lang
	}
	if strings.HasPrefix(t, "#!") {
		return shebangLang(firstLine(t))
	}
	if strings.HasPrefix(t, "<?php") {
		return "php"
	}

	// Formats with an unmistakable shape.
	if (t[0] == '{' || t[0] == '[') && strings.ContainsAny(t, `":`) && strings.Contains(t, `"`) && json.Valid([]byte(t)) {
		return "json"
	}
	if isDiff(t) {
		return "diff"
	}
	if isDockerfile(t) {
		return "dockerfile"
	}

	lines := nonEmptyLines(t)
	if isShell(lines) {
		return "bash"
	}
	if mostlyProse(lines) {
		return ""
	}

	best, bestScore := "", 0.0
	for _, l := range scored {
		score := 0.0
		for _, r := range l.rules {
			n := len(r.re.FindAllStringIndex(t, 3))
			score += r.w * float64(n)
		}
		if score > bestScore {
			best, bestScore = l.id, score
		}
	}
	if bestScore >= 2 {
		return refine(best, t)
	}
	if looksLikeCode(lines) {
		return "code"
	}
	return ""
}

// ---- explicit markers ----

var fenceOpen = regexp.MustCompile("^```([A-Za-z0-9_+#.-]*)[ \t]*\r?\n")

// fenced recognises a whole item wrapped in a Markdown code fence and returns
// the language named on the fence and the code inside it.
func fenced(t string) (lang, body string, ok bool) {
	m := fenceOpen.FindStringSubmatch(t)
	if m == nil || !strings.HasSuffix(t, "```") || len(t) < len(m[0])+3 {
		return "", "", false
	}
	body = strings.TrimSuffix(t[len(m[0]):len(t)-3], "\n")
	if strings.Contains(body, "```") {
		return "", "", false // several fenced blocks with prose between: leave it as text
	}
	lang = aliases[strings.ToLower(m[1])]
	if lang == "" {
		if _, known := langs[strings.ToLower(m[1])]; known {
			lang = strings.ToLower(m[1])
		} else if lang = detectLang(body); lang == "" {
			lang = "code"
		}
	}
	return lang, body, true
}

var aliases = map[string]string{
	"sh": "bash", "shell": "bash", "zsh": "bash", "fish": "bash", "console": "bash", "terminal": "bash",
	"js": "javascript", "jsx": "javascript", "mjs": "javascript", "node": "javascript",
	"ts": "typescript", "tsx": "typescript",
	"py": "python", "python3": "python",
	"golang": "go", "rs": "rust", "rb": "ruby", "kt": "kotlin", "cs": "csharp", "c#": "csharp",
	"c++": "cpp", "cc": "cpp", "h": "c", "hpp": "cpp",
	"yml": "yaml", "ini": "toml", "conf": "toml", "docker": "dockerfile",
	"xml": "html", "svg": "html", "htm": "html", "vue": "html",
	"ps1": "powershell", "pwsh": "powershell", "ps": "powershell",
	"patch": "diff", "psql": "sql", "mysql": "sql", "sqlite": "sql",
}

func shebangLang(line string) string {
	switch {
	case containsAny(line, "bash", "/sh", "zsh", "dash", "ksh", " sh"):
		return "bash"
	case strings.Contains(line, "python"):
		return "python"
	case strings.Contains(line, "node"), strings.Contains(line, "deno"), strings.Contains(line, "bun"):
		return "javascript"
	case strings.Contains(line, "ruby"):
		return "ruby"
	case strings.Contains(line, "php"):
		return "php"
	case strings.Contains(line, "pwsh"):
		return "powershell"
	}
	return "code"
}

var diffHunk = regexp.MustCompile(`(?m)^@@ -\d+(,\d+)? \+\d+(,\d+)? @@`)

func isDiff(t string) bool {
	return strings.HasPrefix(t, "diff --git ") || diffHunk.MatchString(t) ||
		(strings.Contains(t, "\n+++ ") && strings.HasPrefix(t, "--- "))
}

var (
	dockerFrom  = regexp.MustCompile(`(?m)^FROM [\w./:@-]+( AS \w+)?\s*$`)
	dockerOther = regexp.MustCompile(`(?m)^(RUN|COPY|ADD|CMD|ENTRYPOINT|WORKDIR|ENV|EXPOSE|ARG|VOLUME|USER|LABEL) \S`)
)

func isDockerfile(t string) bool {
	return dockerFrom.MatchString(t) && dockerOther.MatchString(t)
}

// ---- shell commands ----

// Commands that are also everyday English words. On their own they prove
// nothing ("make sure", "find me", "time to go"); they need shell syntax
// around them to count.
var ambiguousCmds = wordSet(`make cat cut find sort head tail more less man who time date test kill top touch host watch
	sleep wait read yes which where open start set type go node code history clear exit echo source export
	alias sync mount link split join paste print look tee env free file last write install service screen`)

var shellCmds = wordSet(`sudo doas su apt apt-get dpkg pacman yay paru dnf yum zypper apk brew snap flatpak nix
	git gh docker docker-compose podman kubectl helm terraform ansible vagrant
	systemctl journalctl service loginctl hostnamectl timedatectl
	curl wget ssh scp sftp rsync nc ncat nmap dig nslookup ping traceroute ip ifconfig iptables ufw nft ss netstat
	cd ls ll pwd mkdir rmdir rm cp mv ln chmod chown chgrp stat du df lsblk blkid fdisk parted mkfs umount fsck dd
	grep egrep rg ag sed awk tr xargs wc uniq diff patch tar gzip gunzip zip unzip 7z xz zstd
	ps pkill killall htop btop nohup crontab uname whoami id groups passwd useradd usermod adduser
	npm npx pnpm yarn bun deno pip pip3 pipx python python3 uv poetry conda ruby gem bundle php composer
	cargo rustc rustup gcc g++ clang cmake ninja mvn gradle java javac dotnet swift xcodebuild
	nano vim vi nvim emacs code tmux zellij
	pct qm pvesh pveam pvecm pveversion zfs zpool lxc lxc-attach virsh
	ffmpeg ffprobe convert magick yt-dlp jq yq openssl gpg ssh-keygen ssh-copy-id base64 sha256sum md5sum
	wl-copy wl-paste xclip xdg-open fetch adb fastboot shopify wrangler vercel netlify fly heroku aws gcloud az`)

var shellKeywords = wordSet(`if then else elif fi for while until do done case esac function in`)

var stopwords = wordSet(`is are was were be been am the a an and or but to of in on at for with that this it i you we they
	my your our their me him her them not no so if when what how why can will would should could have has had do does did
	just really very about from into than then there here also`)

var (
	shellSyntax = regexp.MustCompile(`(^|\s)(-{1,2}[A-Za-z][\w-]*|\|{1,2}|&&|>>?|2>&1|<<?)(\s|=|$)|\$[({A-Za-z_]|` + "`" + `[^` + "`" + `]+` + "`" + `|(^|\s)[.~]?/[\w.-]|\\$|;\s*(do|then|done|fi)\b`)
	shellAssign = regexp.MustCompile(`^(export\s+)?[A-Za-z_][A-Za-z0-9_]*=\S*`)
	envPrefix   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S+$`)
	promptLine  = regexp.MustCompile(`^(\$|#|>|[\w.-]+@[\w.-]+[:~][^$#]*[$#])\s+\S`)
	sentenceEnd = regexp.MustCompile(`[.!?…:]["')]?$`)
)

// shellLine reports how sure we are that one line is a shell command:
// 2 = certain, 1 = plausible, 0 = no.
func shellLine(line string) int {
	l := strings.TrimSpace(line)
	if l == "" || strings.HasPrefix(l, "#") {
		return 1 // blank lines and comments ride along with whatever surrounds them
	}
	if m := promptLine.FindStringSubmatch(l); m != nil && (m[1] == "$" || strings.Contains(m[1], "@")) {
		return 2 // "$ command" copied from a terminal or a README
	}
	words := strings.Fields(l)
	// Skip variables set just for this command: FOO=bar command --flag
	for len(words) > 1 && envPrefix.MatchString(words[0]) {
		words = words[1:]
	}
	first := strings.TrimLeft(words[0], "(")
	syntax := shellSyntax.MatchString(l)

	if shellKeywords[first] || first == "fi" || first == "done" || first == "esac" || first == "}" {
		if syntax || len(words) == 1 || strings.Contains(l, "[") || strings.Contains(l, ";") {
			return 2
		}
		return 0
	}
	if shellAssign.MatchString(l) && !strings.Contains(strings.SplitN(l, "=", 2)[0], " ") && len(words) <= 2 || strings.HasPrefix(l, "export ") && strings.Contains(l, "=") {
		return 2
	}
	if first == "sudo" || first == "doas" {
		if len(words) < 2 {
			return 0
		}
		first = words[1]
		words = words[1:]
	}
	// A path to a script counts as a command: ./install.sh, ~/bin/thing
	isPath := strings.HasPrefix(first, "./") || strings.HasPrefix(first, "~/") || (strings.HasPrefix(first, "/") && strings.Count(first, "/") > 1)
	known := shellCmds[first] || ambiguousCmds[first]
	if !known && !isPath {
		return 0
	}
	if syntax {
		return 2
	}
	if ambiguousCmds[first] && !shellCmds[first] {
		return 0
	}
	// A bare command with plain arguments: "git status", "docker ps". Reject
	// anything that reads like a sentence that happens to start with one.
	if len(words) > 6 || sentenceEnd.MatchString(l) || strings.ContainsAny(l, ",") {
		return 0
	}
	for _, w := range words[1:] {
		if stopwords[strings.ToLower(w)] {
			return 0
		}
	}
	if len(words) == 1 {
		return 1 // "ls" alone is a command, but too thin to carry a whole item
	}
	return 2
}

func isShell(lines []string) bool {
	sure, maybe := 0, 0
	for _, l := range lines {
		switch shellLine(l) {
		case 2:
			sure++
		case 1:
			maybe++
		}
	}
	// Every line has to fit, and at least one has to be unmistakable.
	return sure > 0 && sure+maybe == len(lines)
}

// ---- prose and generic code ----

var (
	codeChars = regexp.MustCompile("[{}()\\[\\];=<>$|\\\\`]|->|::|\\w\\.\\w+\\(")
	wordy     = regexp.MustCompile(`^[\p{L}\p{N}'’"“”.,!?:;()\s/&%€$@#+*-]+$`)
)

// proseLine reports whether a line reads as ordinary writing.
func proseLine(l string) bool {
	l = strings.TrimSpace(l)
	words := strings.Fields(l)
	if len(words) < 3 {
		return false // too short to tell either way
	}
	if !codeChars.MatchString(l) {
		return true
	}
	// Sentences may contain a bracket or two; code rarely ends in a full stop
	// after several plain words.
	return wordy.MatchString(l) && sentenceEnd.MatchString(l) && len(words) >= 5 && !strings.HasSuffix(l, ":")
}

func mostlyProse(lines []string) bool {
	prose := 0
	for _, l := range lines {
		if proseLine(l) {
			prose++
		}
	}
	return prose*3 > len(lines) // more than a third of the lines are sentences
}

var codeLine = regexp.MustCompile(`[;{}]\s*$|^\s*[}\])]|\)\s*\{|=>|:=|[!=]==?|\w\(.*\)|^\s{2,}\S|^\t+\S|^\s*(//|/\*|\*/|#\s?\w+)|</?\w+[^>]*>|^\s*[\w."'-]+\s*[:=]\s*\S`)

// looksLikeCode is the fallback for languages without rules of their own:
// several lines, nearly all of them shaped like code.
func looksLikeCode(lines []string) bool {
	if len(lines) < 3 {
		return false
	}
	hits, punct := 0, 0
	for _, l := range lines {
		if codeLine.MatchString(l) {
			hits++
		}
		if strings.ContainsAny(l, "{}();=") {
			punct++
		}
	}
	return hits*5 >= len(lines)*4 && punct*2 >= len(lines)
}

// ---- language scoring ----

type rule struct {
	re *regexp.Regexp
	w  float64
}

type scoredLang struct {
	id    string
	rules []rule
}

func rules(pairs ...any) []rule {
	var out []rule
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, rule{regexp.MustCompile(`(?m)` + pairs[i].(string)), pairs[i+1].(float64)})
	}
	return out
}

// Each rule is a pattern and what one match is worth (up to three matches
// count). A language needs 2 points, so one unmistakable construct or two
// suggestive ones.
var scored = []scoredLang{
	{"python", rules(
		`^\s*def \w+\(.*\)\s*(->\s*[\w\[\], .]+)?:\s*$`, 2.0,
		`^\s*from [\w.]+ import [\w*, ()]+\s*$`, 2.0,
		`^\s*import [\w.]+( as \w+)?(, ?[\w.]+)*\s*$`, 1.5,
		`^\s*(if|elif|while|for|with|try|except|else|class) ?.*:\s*$`, 1.0,
		`\bprint\(|\bself\.|\b(None|True|False)\b|\blambda \w*:|__\w+__|\bf"|\bf'`, 1.0,
		`^\s*@\w+(\(.*\))?\s*$`, 0.5,
	)},
	{"javascript", rules(
		`\b(const|let|var) [\w${}\[\], ]+ = `, 1.5,
		`\bfunction\*? ?\w*\(.*\)\s*\{`, 2.0,
		`=>\s*[{(\w]`, 1.0,
		`\b(console\.(log|error|warn)|document\.|window\.|require|JSON\.(parse|stringify)|addEventListener|module\.exports)\b`, 1.5,
		`^\s*(import .+ from ['"].+['"]|export (default |const |function |class ))`, 2.0,
		`===|!==|\bawait \w|\basync \(|\basync function`, 1.0,
	)},
	{"go", rules(
		`^package \w+\s*$`, 2.0,
		`^func (\(\w+ \*?\w+\) )?\w+\(.*\)`, 2.0,
		`\w+(, \w+)* := `, 1.0,
		`\bfmt\.\w+\(|\berr != nil\b|^import \(|\bdefer \w|\bgo func\(|\bchan \w`, 1.5,
	)},
	{"rust", rules(
		`\b(pub )?fn \w+(<.*>)?\(.*\)( -> [\w<>&\[\]:, ']+)?\s*(\{|where)`, 2.0,
		`\blet mut \w+|\bimpl(<.*>)? \w+|\buse (std|crate|super)::|\bprintln!\(|\b(Some|Ok|Err)\(|#\[derive\(`, 1.5,
		`\bmatch \w+ \{|::<|&mut \w|&str\b|\bVec<`, 1.0,
	)},
	{"cpp", rules(
		`^\s*#include\s*[<"][\w./]+[>"]`, 2.0,
		`\bint main\s*\(`, 2.0,
		`\bstd::\w+|\bcout\s*<<|\bcin\s*>>|\btemplate\s*<|\bnamespace \w+|\bnullptr\b`, 1.5,
		`\b(printf|scanf|malloc|free|sizeof)\(|\b(int|char|float|double|void|unsigned|size_t) \*?\w+(\[\w*\])?\s*(=|;|\()`, 1.0,
	)},
	{"java", rules(
		`\bpublic (static )?(final )?(class|interface|enum|void) \w+`, 2.0,
		`\bSystem\.(out|err)\.print|\bpublic static void main\(|^import java[x]?\.`, 2.0,
		`\b(private|protected) (static )?(final )?[\w<>\[\]]+ \w+\s*(=|;|\()|\bnew \w+(<.*>)?\(|@Override\b`, 1.0,
	)},
	{"csharp", rules(
		`^using System(\.[\w.]+)?;`, 2.0,
		`\bConsole\.(Write|Read)(Line)?\(|\{ get; (set; )?\}|\bnamespace [\w.]+\s*[{;]`, 2.0,
		`\b(public|private|internal) (static )?(async )?(void|string|int|bool|Task(<.*>)?|var) \w+\(`, 1.0,
	)},
	{"swift", rules(
		`^import (SwiftUI|UIKit|Foundation|Combine|AppKit)\s*$`, 2.0,
		`\bfunc \w+\(.*\)( (async )?(throws )?-> [\w\[\]<>?: ]+)?\s*\{`, 2.0,
		`\b(guard|if) let \w+|\b(let|var) \w+: \[?\w+\]?\??\s*(=|$|\{)|@(State|Published|MainActor|Binding|objc)\b|\bstruct \w+: \w+`, 1.5,
	)},
	{"kotlin", rules(
		`\bfun \w+\(.*\)(: [\w<>?]+)?\s*[{=]`, 2.0,
		`\b(val|var) \w+(: [\w<>?]+)? = |\bprintln\(|\bdata class \w+\(|\bwhen \(.*\) \{`, 1.0,
	)},
	{"ruby", rules(
		`^\s*def \w+[?!]?(\(.*\))?\s*$`, 1.5,
		`^\s*end\s*$`, 0.7,
		`\bputs ["'\w]|^\s*require ['"]|\bdo \|\w+(, \w+)*\||\.each\b|\battr_(accessor|reader|writer) :|\bnil\b|=> ?:`, 1.0,
	)},
	{"php", rules(
		`\$\w+ = |\$this->|\becho ["'$]`, 1.0,
		`\bfunction \w+\(.*\$\w+|->\w+\(|\barray\(|=> \$`, 1.0,
	)},
	{"powershell", rules(
		`\b(Get|Set|New|Remove|Add|Start|Stop|Restart|Invoke|Write|Import|Export|Test|Select|Where|ForEach|Out|Install|Enable|Disable)-[A-Z]\w+`, 2.0,
		`\$env:\w+|\$_\b|\s-(eq|ne|gt|lt|like|match|contains)\s|\[\w+(\.\w+)*\]::`, 1.5,
		`^\s*\$\w+ = `, 0.7,
	)},
	{"sql", rules(
		`(?i)^\s*(select\s+.+\s+from|insert\s+into|update\s+\w+\s+set|delete\s+from|create\s+(table|index|view|database)|alter\s+table|drop\s+(table|index|view|database))\b`, 1.5,
		`\b(SELECT|INSERT INTO|UPDATE|DELETE FROM|CREATE TABLE|ALTER TABLE|WHERE|GROUP BY|ORDER BY|LEFT JOIN|INNER JOIN|VALUES|PRIMARY KEY)\b`, 1.0,
		`(?i)\b(from|where|join)\b.*(\*|=|;)|\*\s+from\b|;\s*$`, 0.5,
	)},
	{"html", rules(
		`(?i)<!doctype html|<html[ >]|</(div|span|p|a|ul|li|body|head|script|style|section|table|button|form|h[1-6])>`, 2.0,
		`<\w+(\s+[\w:-]+(="[^"]*")?)*\s*/?>`, 0.7,
		`\{%-?\s*\w+.*?-?%\}|\{\{-?\s*[\w.]+.*?-?\}\}`, 1.0,
	)},
	{"css", rules(
		`^\s*[.#@:\w][\w\s.#:>,*\[\]="'()-]*\{\s*$`, 1.0,
		`^\s*[a-z-]+:\s*[^;{}]+;\s*$`, 1.0,
		`@media |@import |@keyframes |!important|:root|:hover|\bvar\(--`, 1.0,
	)},
	{"yaml", rules(
		`^---\s*$`, 1.0,
		`^[\w.-]+:\s*$\n^\s+(- )?[\w."'-]+`, 1.5,
		`^\s*- [\w.-]+: \S`, 1.0,
		`^(version|services|name|on|jobs|steps|image|ports|volumes|environment|apiVersion|kind|metadata|spec):`, 1.0,
	)},
	{"toml", rules(
		`^\[{1,2}[\w."-]+\]{1,2}\s*$`, 1.5,
		`^[\w.-]+ ?= ?("[^"]*"|'[^']*'|\d+|true|false|\[)`, 0.6,
	)},
}

var (
	tsSigns  = regexp.MustCompile(`(?m):\s*(string|number|boolean|void|any|unknown|never)\b(\[\])?|^\s*(export )?(interface|type) \w+(<.*>)?\s*[={]|\bas (const|string|number|any)\b|\w+<[A-Z]\w*(\[\])?>\(|\b(public|private|readonly) \w+:`)
	cppSigns = regexp.MustCompile(`\bstd::|\bcout\b|\bcin\b|\btemplate\s*<|\bnamespace\b|\bclass \w+|\bnullptr\b|#include\s*<(iostream|vector|string|map|memory|algorithm)>`)
	liquid   = regexp.MustCompile(`\{%-?\s*(if|for|assign|capture|render|include|section|schema|endif|endfor|unless|case|comment|liquid|paginate|form|style)\b`)
)

// refine separates languages that share most of their syntax.
func refine(lang, t string) string {
	switch lang {
	case "javascript":
		if len(tsSigns.FindAllStringIndex(t, 2)) >= 1 {
			return "typescript"
		}
	case "cpp":
		if !cppSigns.MatchString(t) {
			return "c"
		}
	case "html":
		if liquid.MatchString(t) {
			return "liquid"
		}
	}
	return lang
}

// ---- small helpers ----

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimRight(l, "\r"))
		}
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
