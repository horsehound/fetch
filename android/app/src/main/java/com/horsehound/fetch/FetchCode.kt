package com.horsehound.fetch

import java.util.regex.Pattern

/** How one language is highlighted. */
internal class Spec(
    val mode: String = "generic",
    val line: List<String> = emptyList(),
    val block: List<Pair<String, String>> = emptyList(),
    val quotes: String = "",
    val triple: Boolean = false,
    val keywords: Set<String> = emptySet(),
    val types: Set<String> = emptySet(),
    val nocase: Boolean = false,
    val dollar: Boolean = false,
    val preproc: Boolean = false,
    val cmdlets: Boolean = false,
)

internal fun wordSet(s: String): Set<String> = FetchCode.fields(s).toHashSet()

/**
 * Recognises code in a text item, names its language and splits it into
 * coloured tokens. A port of hub/web/fetch-code.js (the reference, which the
 * web page, the desktop app and the iPhone app run), checked against the same
 * expected results in spec/code-corpus.json. The hub can't do this any more:
 * it can't read the text.
 *
 * Classes: kw keyword · str string · com comment · num number · fn function
 * or command · typ type · var variable · key property, tag or key · attr
 * option or attribute · meta prompt, decorator, marker · add / del diff
 * lines. The token texts joined are exactly the input.
 */
object FetchCode {
    data class Result(val lang: String, val label: String, val tokens: List<Pair<String, String>>)

    /** null for ordinary text. */
    fun analyze(text: String): Result? {
        val lang = detect(text)
        if (lang.isEmpty()) return null
        return Result(lang, label(lang), highlight(text, lang))
    }

    val languages: List<String> get() = CodeTables.LANGS.keys.toList()

    fun label(id: String): String = CodeTables.LABELS[id] ?: (id.take(1).uppercase() + id.drop(1))

    // ---- matching the reference exactly ----

    // The rules were written for Go's regexp package. goRe pins down every
    // place where Java's (and Android's ICU) regular expressions differ from
    // it and from the JavaScript reference: \s, \w, \d and \b are spelled out
    // in ASCII, . and multi-line ^ and $ only know \n as a line break, a
    // single-line $ is the very end, and (?i) folds ASCII letters only.
    private const val GO_SPACE = "\\t\\n\\f\\r "
    private const val WORD = "A-Za-z0-9_"
    private const val BOUNDARY = "(?:(?<=[$WORD])(?![$WORD])|(?<![$WORD])(?=[$WORD]))"

    internal fun goRe(source: String): Pattern {
        var src = source
        var nocase = false
        var multiLine = false
        while (true) {
            val m = Regex("^\\(\\?([im]+)\\)").find(src) ?: break
            if ('i' in m.groupValues[1]) nocase = true
            if ('m' in m.groupValues[1]) multiLine = true
            src = src.substring(m.value.length)
        }
        fun isLetter(c: Char) = c in 'a'..'z' || c in 'A'..'Z'
        fun other(c: Char) = if (c in 'a'..'z') c.uppercaseChar() else c.lowercaseChar()

        val out = StringBuilder()
        var inClass = false
        var i = 0
        while (i < src.length) {
            val c = src[i]
            if (c == '\\') {
                val n = src[i + 1]
                when (n) {
                    's' -> out.append(if (inClass) GO_SPACE else "[$GO_SPACE]")
                    'S' -> out.append("[^$GO_SPACE]")
                    'w' -> out.append(if (inClass) WORD else "[$WORD]")
                    'd' -> out.append(if (inClass) "0-9" else "[0-9]")
                    'b' -> out.append(BOUNDARY)
                    'p', 'P' -> {
                        val end = src.indexOf('}', i)
                        out.append(src, i, end + 1)
                        i = end + 1
                        continue
                    }
                    else -> out.append(c).append(n)
                }
                i += 2
                continue
            }
            if (inClass) {
                when {
                    c == ']' -> {
                        inClass = false
                        out.append(c)
                    }
                    // Literal in Go; nesting and intersection in Java.
                    c == '[' || c == '&' -> out.append('\\').append(c)
                    nocase && isLetter(c) -> {
                        if (i + 2 < src.length && src[i + 1] == '-' && isLetter(src[i + 2])) {
                            val e = src[i + 2]
                            out.append(c).append('-').append(e).append(other(c)).append('-').append(other(e))
                            i += 3
                            continue
                        }
                        out.append(c).append(other(c))
                    }
                    else -> out.append(c)
                }
                i++
                continue
            }
            when {
                c == '[' -> {
                    inClass = true
                    out.append(c)
                    if (i + 1 < src.length && src[i + 1] == '^') {
                        out.append('^')
                        i++
                    }
                    if (i + 1 < src.length && src[i + 1] == ']') {
                        out.append("\\]")
                        i++
                    }
                }
                c == '.' -> out.append("[^\\n]")
                c == '^' -> out.append(if (multiLine) "(?<![^\\n])" else "^")
                c == '$' -> out.append(if (multiLine) "(?![^\\n])" else "\\z")
                nocase && isLetter(c) -> out.append('[').append(c).append(other(c)).append(']')
                else -> out.append(c)
            }
            i++
        }
        return Pattern.compile(out.toString())
    }

    private fun Pattern.test(s: String) = matcher(s).find()

    private fun countMatches(re: Pattern, s: String, max: Int): Int {
        val m = re.matcher(s)
        var n = 0
        while (n < max && m.find()) n++
        return n
    }

    // Go's unicode.IsSpace.
    private fun isGoSpace(c: Char) = c == '\t' || c == '\n' || c == '\u000b' || c == '\u000c' || c == '\r' ||
        c == ' ' || c == '\u0085' || c == '\u00a0' || c == '\u1680' || c in '\u2000'..'\u200a' ||
        c == '\u2028' || c == '\u2029' || c == '\u202f' || c == '\u205f' || c == '\u3000'

    internal fun trimSpace(s: String): String {
        var a = 0
        var b = s.length
        while (a < b && isGoSpace(s[a])) a++
        while (b > a && isGoSpace(s[b - 1])) b--
        return s.substring(a, b)
    }

    internal fun fields(s: String): List<String> {
        val out = ArrayList<String>()
        var start = -1
        for (i in s.indices) {
            if (isGoSpace(s[i])) {
                if (start >= 0) out.add(s.substring(start, i))
                start = -1
            } else if (start < 0) {
                start = i
            }
        }
        if (start >= 0) out.add(s.substring(start))
        return out
    }

    private fun utf8Length(s: String): Int {
        var n = 0
        var i = 0
        while (i < s.length) {
            val c = s[i].code
            when {
                c < 0x80 -> n += 1
                c < 0x800 -> n += 2
                c in 0xd800..0xdbff && i + 1 < s.length -> {
                    n += 4
                    i++
                }
                else -> n += 3
            }
            i++
        }
        return n
    }

    // ---- detection ----

    private const val MAX_CODE_LEN = 64 shl 10

    fun detect(text: String): String {
        val t = trimSpace(text)
        if (t.isEmpty() || utf8Length(text) > MAX_CODE_LEN) return ""

        fenced(t)?.let { return it.first }
        if (t.startsWith("#!")) return shebangLang(t.substringBefore('\n'))
        if (t.startsWith("<?php")) return "php"

        if ((t[0] == '{' || t[0] == '[') && (t.contains('"') || t.contains(':')) && t.contains('"') && Json.isValid(t)) return "json"
        if (isDiff(t)) return "diff"
        if (isDockerfile(t)) return "dockerfile"

        val lines = nonEmptyLines(t)
        if (isShell(lines)) return "bash"
        if (mostlyProse(lines)) return ""

        var best = ""
        var bestScore = 0.0
        for ((id, rules) in scored) {
            var score = 0.0
            for ((re, w) in rules) score += w * countMatches(re, t, 3)
            if (score > bestScore) {
                best = id
                bestScore = score
            }
        }
        if (bestScore >= 2) return refine(best, t)
        if (looksLikeCode(lines)) return "code"
        return ""
    }

    private val FENCE_OPEN = Pattern.compile("^```([A-Za-z0-9_+#.-]*)[ \\t]*\\r?\\n")

    /** A fenced block: its language and its body. */
    private fun fenced(t: String): Pair<String, String>? {
        val m = FENCE_OPEN.matcher(t)
        if (!m.lookingAt()) return null
        val open = m.end()
        if (!t.endsWith("```") || t.length < open + 3) return null
        var body = t.substring(open, t.length - 3)
        if (body.endsWith("\n")) body = body.dropLast(1)
        if (body.contains("```")) return null
        val name = m.group(1).lowercase()
        var lang = CodeTables.ALIASES[name] ?: ""
        if (lang.isEmpty()) {
            lang = if (CodeTables.LANGS.containsKey(name)) name else detect(body).ifEmpty { "code" }
        }
        return lang to body
    }

    private fun shebangLang(line: String): String = when {
        listOf("bash", "/sh", "zsh", "dash", "ksh", " sh").any { line.contains(it) } -> "bash"
        line.contains("python") -> "python"
        line.contains("node") || line.contains("deno") || line.contains("bun") -> "javascript"
        line.contains("ruby") -> "ruby"
        line.contains("php") -> "php"
        line.contains("pwsh") -> "powershell"
        else -> "code"
    }

    private val DIFF_HUNK = goRe(CodeTables.DIFF_HUNK)
    private fun isDiff(t: String) =
        t.startsWith("diff --git ") || DIFF_HUNK.test(t) || (t.contains("\n+++ ") && t.startsWith("--- "))

    private val DOCKER_FROM = goRe(CodeTables.DOCKER_FROM)
    private val DOCKER_OTHER = goRe(CodeTables.DOCKER_OTHER)
    private fun isDockerfile(t: String) = DOCKER_FROM.test(t) && DOCKER_OTHER.test(t)

    // ---- shell commands ----

    private val SHELL_SYNTAX = goRe(CodeTables.SHELL_SYNTAX)
    private val SHELL_ASSIGN = goRe(CodeTables.SHELL_ASSIGN)
    private val ENV_PREFIX = goRe(CodeTables.ENV_PREFIX)
    private val PROMPT_LINE = goRe(CodeTables.PROMPT_LINE)
    private val SENTENCE_END = goRe(CodeTables.SENTENCE_END)

    /** 2 = certainly a shell command, 1 = plausible, 0 = no. */
    private fun shellLine(line: String): Int {
        val l = trimSpace(line)
        if (l.isEmpty() || l.startsWith("#")) return 1
        val m = PROMPT_LINE.matcher(l)
        if (m.find() && (m.group(1) == "$" || m.group(1).contains('@'))) return 2
        var words = fields(l)
        while (words.size > 1 && ENV_PREFIX.test(words[0])) words = words.drop(1)
        var first = words[0].trimStart('(')
        val syntax = SHELL_SYNTAX.test(l)

        if (first in CodeTables.SHELL_KEYWORDS || first == "fi" || first == "done" || first == "esac" || first == "}") {
            if (syntax || words.size == 1 || l.contains('[') || l.contains(';')) return 2
            return 0
        }
        if ((SHELL_ASSIGN.test(l) && !l.substringBefore('=').contains(' ') && words.size <= 2) ||
            (l.startsWith("export ") && l.contains('='))
        ) return 2
        if (first == "sudo" || first == "doas") {
            if (words.size < 2) return 0
            first = words[1]
            words = words.drop(1)
        }
        val isPath = first.startsWith("./") || first.startsWith("~/") || (first.startsWith("/") && first.count { it == '/' } > 1)
        val known = first in CodeTables.SHELL_CMDS || first in CodeTables.AMBIGUOUS_CMDS
        if (!known && !isPath) return 0
        if (syntax) return 2
        if (first in CodeTables.AMBIGUOUS_CMDS && first !in CodeTables.SHELL_CMDS) return 0
        if (words.size > 6 || SENTENCE_END.test(l) || l.contains(',')) return 0
        for (i in 1 until words.size) {
            if (words[i].lowercase() in CodeTables.STOPWORDS) return 0
        }
        return if (words.size == 1) 1 else 2
    }

    private fun isShell(lines: List<String>): Boolean {
        var sure = 0
        var maybe = 0
        for (l in lines) {
            when (shellLine(l)) {
                2 -> sure++
                1 -> maybe++
            }
        }
        return sure > 0 && sure + maybe == lines.size
    }

    // ---- prose and generic code ----

    private val CODE_CHARS = goRe(CodeTables.CODE_CHARS)
    private val WORDY = goRe(CodeTables.WORDY)

    private fun proseLine(line: String): Boolean {
        val l = trimSpace(line)
        val words = fields(l)
        if (words.size < 3) return false
        if (!CODE_CHARS.test(l)) return true
        return WORDY.test(l) && SENTENCE_END.test(l) && words.size >= 5 && !l.endsWith(":")
    }

    private fun mostlyProse(lines: List<String>) = lines.count { proseLine(it) } * 3 > lines.size

    private val CODE_LINE = goRe(CodeTables.CODE_LINE)

    private fun looksLikeCode(lines: List<String>): Boolean {
        if (lines.size < 3) return false
        var hits = 0
        var punct = 0
        for (l in lines) {
            if (CODE_LINE.test(l)) hits++
            if (l.any { it in "{}();=" }) punct++
        }
        return hits * 5 >= lines.size * 4 && punct * 2 >= lines.size
    }

    // ---- language scoring ----

    private val scored: List<Pair<String, List<Pair<Pattern, Double>>>> =
        CodeTables.SCORED.map { (id, rules) -> id to rules.map { (src, w) -> goRe(src) to w } }

    private val TS_SIGNS = goRe(CodeTables.TS_SIGNS)
    private val CPP_SIGNS = goRe(CodeTables.CPP_SIGNS)
    private val LIQUID = goRe(CodeTables.LIQUID)

    private fun refine(lang: String, t: String): String = when {
        lang == "javascript" && TS_SIGNS.test(t) -> "typescript"
        lang == "cpp" && !CPP_SIGNS.test(t) -> "c"
        lang == "html" && LIQUID.test(t) -> "liquid"
        else -> lang
    }

    private fun nonEmptyLines(s: String) =
        s.split('\n').filter { trimSpace(it).isNotEmpty() }.map { it.trimEnd('\r') }

    // ---- highlighting ----

    private class Toks {
        val classes = ArrayList<String>()
        val texts = ArrayList<StringBuilder>()

        fun add(cls: String, text: String) {
            if (text.isEmpty()) return
            if (classes.isNotEmpty() && classes.last() == cls) {
                texts.last().append(text)
            } else {
                classes.add(cls)
                texts.add(StringBuilder(text))
            }
        }

        fun list(): List<Pair<String, String>> = classes.indices.map { classes[it] to texts[it].toString() }
    }

    fun highlight(text: String, lang: String): List<Pair<String, String>> {
        val sp = CodeTables.LANGS[lang] ?: CodeTables.LANGS.getValue("code")
        val out = Toks()
        val f = fenced(trimSpace(text))
        if (f != null) {
            val body = f.second
            val start = text.indexOf(body)
            if (body.isNotEmpty() && start >= 0) {
                out.add("meta", text.substring(0, start))
                lex(body, sp, out)
                out.add("meta", text.substring(start + body.length))
                return out.list()
            }
        }
        lex(text, sp, out)
        return out.list()
    }

    private fun lex(s: String, sp: Spec, out: Toks) {
        when (sp.mode) {
            "shell" -> lexShell(s, out)
            "docker" -> lexDocker(s, out)
            "json" -> lexJson(s, out)
            "markup" -> lexMarkup(s, out)
            "css" -> lexCss(s, out)
            "yaml" -> lexLines(s, out, ::yamlLine)
            "ini" -> lexLines(s, out, ::iniLine)
            "diff" -> lexLines(s, out, ::diffLine)
            else -> lexGeneric(s, sp, out)
        }
    }

    // Character classes, by UTF-16 code unit. Everything outside ASCII counts
    // as a letter, as it does (byte by byte) in the reference implementation.
    private fun isIdentStart(c: Char) = c == '_' || c in 'a'..'z' || c in 'A'..'Z' || c.code >= 0x80
    private fun isDigit(c: Char) = c in '0'..'9'
    private fun isIdent(c: Char) = isIdentStart(c) || isDigit(c)
    private fun isSpace(c: Char) = c == ' ' || c == '\t' || c == '\n' || c == '\r'

    private fun lineEnd(s: String, i: Int): Int {
        val j = s.indexOf('\n', i)
        return if (j >= 0) j else s.length
    }

    private fun stringEnd(s: String, i: Int, multiLine: Boolean): Int {
        val q = s[i]
        var j = i + 1
        while (j < s.length) {
            val c = s[j]
            if (c == '\\') j++
            else if (c == q) return j + 1
            else if (c == '\n' && !multiLine && q != '`') return -1
            j++
        }
        return -1
    }

    private fun numberEnd(s: String, i: Int): Int {
        var j = i
        while (j < s.length) {
            val c = s[j]
            if (isDigit(c) || (isIdentStart(c) && c.code < 0x80) || (c == '.' && j + 1 < s.length && isDigit(s[j + 1]))) j++ else break
        }
        return j
    }

    private fun identEnd(s: String, i: Int): Int {
        var j = i
        while (j < s.length && isIdent(s[j])) j++
        return j
    }

    private fun lexGeneric(s: String, sp: Spec, out: Toks) {
        var lineStart = true
        var i = 0
        while (i < s.length) {
            val c = s[i]
            var j: Int

            if (sp.preproc && lineStart && c == '#') {
                j = identEnd(s, i + 1)
                out.add("meta", s.substring(i, j))
                i = j
                lineStart = false
                continue
            }
            val p = sp.line.firstOrNull { s.startsWith(it, i) } ?: ""
            if (p.isNotEmpty() && !(p == "#" && i > 0 && !isSpace(s[i - 1])) && !(p == "//" && i > 0 && s[i - 1] == ':')) {
                j = lineEnd(s, i)
                out.add("com", s.substring(i, j))
                i = j
                continue
            }
            val b = sp.block.firstOrNull { s.startsWith(it.first, i) }
            if (b != null) {
                j = s.length
                val close = s.indexOf(b.second, i + b.first.length)
                if (close >= 0) j = close + b.second.length
                out.add("com", s.substring(i, j))
                i = j
                continue
            }
            if (sp.triple && (s.startsWith("\"\"\"", i) || s.startsWith("'''", i))) {
                j = s.length
                val end3 = s.indexOf(s.substring(i, i + 3), i + 3)
                if (end3 >= 0) j = end3 + 3
                out.add("str", s.substring(i, j))
                i = j
                lineStart = false
                continue
            }
            if (sp.quotes.indexOf(c) >= 0) {
                j = stringEnd(s, i, false)
                if (j > 0) {
                    out.add("str", s.substring(i, j))
                    i = j
                    lineStart = false
                    continue
                }
            }
            if (c == '@' && i + 1 < s.length && isIdentStart(s[i + 1]) && (i == 0 || !isIdent(s[i - 1]))) {
                j = identEnd(s, i + 1)
                out.add("meta", s.substring(i, j))
                i = j
                lineStart = false
                continue
            }
            if (sp.dollar && c == '$' && i + 1 < s.length && (isIdentStart(s[i + 1]) || s[i + 1] == '_')) {
                j = identEnd(s, i + 1)
                if (sp.cmdlets && j < s.length && s[j] == ':' && j + 1 < s.length && isIdentStart(s[j + 1])) j = identEnd(s, j + 1)
                out.add("var", s.substring(i, j))
                i = j
                lineStart = false
                continue
            }
            if (sp.cmdlets && c == '-' && i > 0 && isSpace(s[i - 1]) && i + 1 < s.length && isIdentStart(s[i + 1])) {
                j = identEnd(s, i + 1)
                out.add("attr", s.substring(i, j))
                i = j
                continue
            }
            if (isDigit(c) && (i == 0 || !isIdent(s[i - 1]))) {
                j = numberEnd(s, i)
                out.add("num", s.substring(i, j))
                i = j
                lineStart = false
                continue
            }
            if (isIdentStart(c)) {
                j = identEnd(s, i)
                if (sp.cmdlets) {
                    while (j + 1 < s.length && s[j] == '-' && isIdentStart(s[j + 1])) j = identEnd(s, j + 1)
                }
                val word = s.substring(i, j)
                val look = if (sp.nocase) word.lowercase() else word
                when {
                    look in sp.keywords -> out.add("kw", word)
                    look in sp.types -> out.add("typ", word)
                    sp.cmdlets && word.contains('-') -> out.add("fn", word)
                    j < s.length && s[j] == '(' && !sp.nocase -> out.add("fn", word)
                    else -> out.add("", word)
                }
                i = j
                lineStart = false
                continue
            }
            if (c == '\n') lineStart = true else if (!isSpace(c)) lineStart = false
            out.add("", c.toString())
            i++
        }
    }

    private val KEEP_CMD_POS = setOf("sudo", "doas", "exec", "time", "nohup", "xargs", "env")
    private val ENDS_CMD_POS = setOf("in", "export", "local", "return", "for", "case", "function")

    private fun lexShell(s: String, out: Toks) {
        var cmdPos = true
        var i = 0
        while (i < s.length) {
            val c = s[i]
            val prev = if (i > 0) s[i - 1] else null
            val atWordStart = i == 0 || isSpace(s[i - 1]) || (prev != null && prev in ";|&(")
            val lineStart = i == 0 || prev == '\n'
            var j: Int

            if (c == '\n') {
                out.add("", "\n")
                i++
                cmdPos = true
            } else if (isSpace(c)) {
                out.add("", c.toString())
                i++
            } else if (lineStart && (c == '$' || c == '>') && i + 1 < s.length && s[i + 1] == ' ') {
                out.add("meta", s.substring(i, i + 2))
                i += 2
            } else if (c == '#' && atWordStart) {
                j = lineEnd(s, i)
                out.add("com", s.substring(i, j))
                i = j
            } else if (c == '"' || c == '\'') {
                j = stringEnd(s, i, true)
                if (j < 0) {
                    j = i + 1
                    out.add("", s.substring(i, j))
                } else {
                    out.add("str", s.substring(i, j))
                }
                i = j
                cmdPos = false
            } else if (c == '$' && i + 1 < s.length && s[i + 1] == '(') {
                out.add("", "$(")
                i += 2
                cmdPos = true
            } else if (c == '$' && i + 1 < s.length && s[i + 1] == '{') {
                j = s.length
                val close = s.indexOf('}', i)
                if (close >= 0) j = close + 1
                out.add("var", s.substring(i, j))
                i = j
                cmdPos = false
            } else if (c == '$' && i + 1 < s.length && (isIdent(s[i + 1]) || s[i + 1] in "?@*#!$")) {
                j = i + 2
                if (isIdentStart(s[i + 1])) j = identEnd(s, i + 1)
                out.add("var", s.substring(i, j))
                i = j
                cmdPos = false
            } else if (c == '|' || c == '&' || c == ';' || c == '(' || c == '`') {
                out.add("", c.toString())
                i++
                cmdPos = true
            } else if (c == '-' && atWordStart && i + 1 < s.length && (isIdent(s[i + 1]) || s[i + 1] == '-')) {
                j = i
                while (j < s.length && !isSpace(s[j]) && s[j] != '=' && s[j] !in ";|&)") j++
                out.add("attr", s.substring(i, j))
                i = j
            } else {
                j = i
                while (j < s.length && !isSpace(s[j]) && s[j] !in ";|&()\"'`$") j++
                if (j == i) j = i + 1
                val word = s.substring(i, j)
                if (cmdPos && SHELL_ASSIGN.test(word)) {
                    val eq = word.indexOf('=')
                    out.add("var", word.substring(0, eq))
                    out.add("", word.substring(eq))
                } else if (cmdPos && (word in CodeTables.SHELL_KEYWORDS || word == "export" || word == "local" || word == "return")) {
                    out.add("kw", word)
                    cmdPos = word !in ENDS_CMD_POS
                } else if (cmdPos && word !in setOf("[", "[[", "{", "}", "!")) {
                    out.add("fn", word)
                    cmdPos = word in KEEP_CMD_POS
                } else {
                    out.add("", word)
                }
                i = j
            }
        }
    }

    private fun lexDocker(s: String, out: Toks) {
        lexLines(s, out) { line, o ->
            val trimmed = line.trimStart(' ', '\t')
            val indent = line.substring(0, line.length - trimmed.length)
            val sp = trimmed.indexOf(' ')
            val word = if (sp >= 0) trimmed.substring(0, sp) else trimmed
            val rest = if (sp >= 0) trimmed.substring(sp + 1) else ""
            when {
                trimmed.startsWith("#") -> o.add("com", line)
                word !in CodeTables.DOCKER_INSTR -> lexShell(line, o)
                else -> {
                    o.add("", indent)
                    o.add("kw", word)
                    if (trimmed.length != word.length) {
                        o.add("", " ")
                        if (word == "RUN") {
                            lexShell(rest, o)
                        } else {
                            val sub = Toks()
                            lexShell(rest, sub)
                            for ((cls, text) in sub.list()) o.add(if (cls == "fn") "" else cls, text)
                        }
                    }
                }
            }
        }
    }

    private fun lexLines(s: String, out: Toks, each: (String, Toks) -> Unit) {
        var rest = s
        while (rest.isNotEmpty()) {
            val j = rest.indexOf('\n')
            if (j < 0) {
                each(rest, out)
                return
            }
            each(rest.substring(0, j), out)
            out.add("", "\n")
            rest = rest.substring(j + 1)
        }
    }

    private fun scalar(v: String, out: Toks) {
        var a = 0
        while (a < v.length && isGoSpace(v[a])) a++
        val lead = v.substring(0, a)
        var t = v.substring(a)
        var b = t.length
        while (b > 0 && isGoSpace(t[b - 1])) b--
        val trail = t.substring(b)
        t = t.substring(0, b)

        var comment = ""
        if (!t.startsWith("\"") && !t.startsWith("'")) {
            val k = t.indexOf(" #")
            if (k >= 0) {
                comment = t.substring(k)
                t = t.substring(0, k)
            }
        }
        out.add("", lead)
        val lower = t.lowercase()
        when {
            t.isEmpty() -> {}
            t[0] == '"' || t[0] == '\'' -> out.add("str", t)
            lower in setOf("true", "false", "null", "yes", "no", "~") -> out.add("kw", t)
            (isDigit(t[0]) && numberEnd(t, 0) == t.length) ||
                (t[0] == '-' && t.length > 1 && isDigit(t[1]) && numberEnd(t, 1) == t.length) -> out.add("num", t)
            else -> out.add("", t)
        }
        out.add("com", comment)
        out.add("", trail)
    }

    private fun yamlLine(line: String, out: Toks) {
        var trimmed = line.trimStart(' ', '\t')
        out.add("", line.substring(0, line.length - trimmed.length))
        if (trimmed.startsWith("#")) {
            out.add("com", trimmed)
            return
        }
        if (trimmed == "---" || trimmed == "...") {
            out.add("meta", trimmed)
            return
        }
        while (trimmed.startsWith("- ")) {
            out.add("", "- ")
            trimmed = trimmed.substring(2)
        }
        val k = trimmed.indexOf(':')
        if (k > 0 && (k == trimmed.length - 1 || trimmed[k + 1] == ' ') && trimmed[0] !in "\"'[{") {
            out.add("key", trimmed.substring(0, k))
            out.add("", ":")
            scalar(trimmed.substring(k + 1), out)
            return
        }
        scalar(trimmed, out)
    }

    private fun iniLine(line: String, out: Toks) {
        val trimmed = trimSpace(line)
        when {
            trimmed.startsWith("#") || trimmed.startsWith(";") -> out.add("com", line)
            trimmed.startsWith("[") -> out.add("kw", line)
            else -> {
                val k = line.indexOf('=')
                if (k > 0) {
                    out.add("key", line.substring(0, k))
                    out.add("", "=")
                    scalar(line.substring(k + 1), out)
                    return
                }
                out.add("", line)
            }
        }
    }

    private fun diffLine(line: String, out: Toks) {
        when {
            line.startsWith("+++") || line.startsWith("---") || line.startsWith("diff ") || line.startsWith("index ") -> out.add("com", line)
            line.startsWith("@@") -> out.add("meta", line)
            line.startsWith("+") -> out.add("add", line)
            line.startsWith("-") -> out.add("del", line)
            else -> out.add("", line)
        }
    }

    private fun lexJson(s: String, out: Toks) {
        var i = 0
        while (i < s.length) {
            val c = s[i]
            var j: Int
            if (c == '"') {
                j = stringEnd(s, i, false)
                if (j < 0) j = s.length
                var k = j
                while (k < s.length && isSpace(s[k])) k++
                out.add(if (k < s.length && s[k] == ':') "key" else "str", s.substring(i, j))
                i = j
            } else if (isDigit(c) || (c == '-' && i + 1 < s.length && isDigit(s[i + 1]))) {
                j = numberEnd(s, i + 1)
                out.add("num", s.substring(i, j))
                i = j
            } else if (isIdentStart(c)) {
                j = identEnd(s, i)
                out.add("kw", s.substring(i, j))
                i = j
            } else {
                out.add("", c.toString())
                i++
            }
        }
    }

    private fun templateEnd(s: String, k: Int): Int {
        val closer = if (k + 1 < s.length && s[k + 1] == '{') "}}" else "%}"
        val m = s.indexOf(closer, k)
        return if (m >= 0) m + 2 else s.length
    }

    private fun lexMarkup(s: String, out: Toks) {
        var i = 0
        while (i < s.length) {
            val j: Int
            if (s.startsWith("<!--", i)) {
                val k = s.indexOf("-->", i)
                j = if (k >= 0) k + 3 else s.length
                out.add("com", s.substring(i, j))
                i = j
            } else if (s.startsWith("{%", i) || s.startsWith("{{", i)) {
                j = templateEnd(s, i)
                lexTemplateTag(s.substring(i, j), out)
                i = j
            } else if (s[i] == '<' && i + 1 < s.length && (isIdentStart(s[i + 1]) || s[i + 1] in "/!?")) {
                i = lexTag(s, i, out)
            } else {
                out.add("", s[i].toString())
                i++
            }
        }
    }

    private fun lexTag(s: String, i: Int, out: Toks): Int {
        var j = i + 1
        while (j < s.length && s[j] in "/!?") j++
        out.add("", s.substring(i, j))
        var k = j
        while (k < s.length && (isIdent(s[k]) || s[k] == '-' || s[k] == ':')) k++
        out.add("key", s.substring(j, k))
        while (k < s.length && s[k] != '>') {
            val c = s[k]
            var e: Int
            if (c == '"' || c == '\'') {
                e = stringEnd(s, k, true)
                if (e < 0) e = s.length
                out.add("str", s.substring(k, e))
                k = e
            } else if (s.startsWith("{{", k) || s.startsWith("{%", k)) {
                e = templateEnd(s, k)
                lexTemplateTag(s.substring(k, e), out)
                k = e
            } else if (isIdentStart(c) || c == '@' || c == ':') {
                e = k + 1
                while (e < s.length && (isIdent(s[e]) || s[e] in "-:.@")) e++
                out.add("attr", s.substring(k, e))
                k = e
            } else {
                out.add("", c.toString())
                k++
            }
        }
        if (k < s.length) {
            out.add("", ">")
            k++
        }
        return k
    }

    private fun lexTemplateTag(tag: String, out: Toks) {
        if (tag.length < 4) {
            out.add("meta", tag)
            return
        }
        val open = tag.substring(0, 2)
        var body = tag.substring(2, tag.length - 2)
        var close = tag.substring(tag.length - 2)
        if (!tag.endsWith("}}") && !tag.endsWith("%}")) {
            body = tag.substring(2)
            close = ""
        }
        out.add("meta", open)
        lexGeneric(body, CodeTables.LIQUID_SPEC, out)
        out.add("meta", close)
    }

    private fun lexCss(s: String, out: Toks) {
        var depth = 0
        var inValue = false
        var i = 0
        while (i < s.length) {
            val c = s[i]
            var j: Int
            if (s.startsWith("/*", i)) {
                val k = s.indexOf("*/", i + 2)
                j = if (k >= 0) k + 2 else s.length
                out.add("com", s.substring(i, j))
                i = j
            } else if (c == '"' || c == '\'') {
                j = stringEnd(s, i, false)
                if (j < 0) {
                    j = i + 1
                    out.add("", s.substring(i, j))
                } else {
                    out.add("str", s.substring(i, j))
                }
                i = j
            } else if (c == '{') {
                depth++
                inValue = false
                out.add("", "{")
                i++
            } else if (c == '}') {
                if (depth > 0) depth--
                inValue = false
                out.add("", "}")
                i++
            } else if (c == ';') {
                inValue = false
                out.add("", ";")
                i++
            } else if (c == '@') {
                j = i + 1
                while (j < s.length && (isIdent(s[j]) || s[j] == '-')) j++
                out.add("kw", s.substring(i, j))
                i = j
            } else if (inValue && (isDigit(c) || (c == '.' && i + 1 < s.length && isDigit(s[i + 1])))) {
                j = i
                while (j < s.length && (isIdent(s[j]) || s[j] == '.' || s[j] == '%')) j++
                out.add("num", s.substring(i, j))
                i = j
            } else if (inValue && c == '#') {
                j = identEnd(s, i + 1)
                out.add("num", s.substring(i, j))
                i = j
            } else if (isIdentStart(c) || (c == '-' && i + 1 < s.length && (isIdentStart(s[i + 1]) || s[i + 1] == '-'))) {
                j = i + 1
                while (j < s.length && (isIdent(s[j]) || s[j] == '-')) j++
                val word = s.substring(i, j)
                var k2 = j
                while (k2 < s.length && (s[k2] == ' ' || s[k2] == '\t')) k2++
                if (depth > 0 && !inValue && k2 < s.length && s[k2] == ':' && !isNestedSelector(s, k2)) {
                    out.add("key", word)
                    out.add("", s.substring(j, k2 + 1))
                    inValue = true
                    j = k2 + 1
                } else if (inValue && j < s.length && s[j] == '(') {
                    out.add("fn", word)
                } else if (!inValue) {
                    out.add("typ", word)
                } else {
                    out.add("", word)
                }
                i = j
            } else {
                out.add("", c.toString())
                i++
            }
        }
    }

    private fun isNestedSelector(s: String, colon: Int): Boolean {
        for (k in colon + 1 until s.length) {
            if (s[k] == '{') return true
            if (s[k] == ';' || s[k] == '}') return false
        }
        return false
    }
}
