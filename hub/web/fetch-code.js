/*
 * Recognises code in a text item, names its language and splits it into
 * coloured tokens. Runs on the devices: the hub can't read the text.
 *
 * Used by the web page, the desktop app (which bundles this page) and the
 * iPhone app (through JavaScriptCore); the Android app has a Kotlin port.
 * All of them are checked against spec/code-corpus.json, which holds this
 * algorithm's expected results.
 *
 * FetchCode.analyze(text) -> null for ordinary text, or
 *   { lang: "bash", label: "Shell", tokens: [[class, text], ...] }
 * Classes: kw keyword · str string · com comment · num number · fn function
 * or command · typ type · var variable · key property, tag or key · attr
 * option or attribute · meta prompt, decorator, marker · add / del diff lines.
 * The token texts joined are exactly the input.
 */
(function (root) {
  'use strict';

  // ---- matching the reference implementation exactly ----
  // The rules below were written for Go's regexp package. goRe translates the
  // places where JavaScript differs: \s and . mean slightly different things,
  // in multi-line mode ^ and $ only see \n as a line break (JavaScript also
  // takes \r and the Unicode separators), and flags are written inside the
  // pattern.
  var GO_SPACE = '\\t\\n\\f\\r ';
  function goRe(src, global) {
    var flags = global ? 'g' : '';
    var multiLine = false;
    for (;;) {
      var m = /^\(\?([im]+)\)/.exec(src);
      if (!m) break;
      if (m[1].indexOf('i') >= 0) flags += 'i';
      if (m[1].indexOf('m') >= 0) multiLine = true;
      src = src.slice(m[0].length);
    }
    if (src.indexOf('\\p{') >= 0) flags += 'u';
    var out = '';
    var inClass = false;
    for (var i = 0; i < src.length; i++) {
      var c = src[i];
      if (c === '\\') {
        var n = src[i + 1];
        if (n === 's') out += inClass ? GO_SPACE : '[' + GO_SPACE + ']';
        else if (n === 'S') out += '[^' + GO_SPACE + ']';
        else out += c + n;
        i++;
        continue;
      }
      if (inClass) {
        if (c === ']') inClass = false;
        out += c;
        continue;
      }
      if (c === '[') {
        inClass = true;
        out += c;
        if (src[i + 1] === '^') { out += '^'; i++; }
        if (src[i + 1] === ']') { out += '\\]'; i++; }
        continue;
      }
      if (c === '.') out += '[^\\n]';
      else if (multiLine && c === '^') out += '(?<![^\\n])';
      else if (multiLine && c === '$') out += '(?![^\\n])';
      else out += c;
    }
    return new RegExp(out, flags);
  }

  function countMatches(re, s, max) {
    re.lastIndex = 0;
    var n = 0;
    while (n < max) {
      var m = re.exec(s);
      if (!m) break;
      n++;
      if (m[0] === '') re.lastIndex++;
    }
    return n;
  }

  // Go's unicode.IsSpace.
  var SPACE_CHARS = '\\t\\n\\u000b\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000';
  var TRIM = new RegExp('^[' + SPACE_CHARS + ']+|[' + SPACE_CHARS + ']+$', 'g');
  var FIELDS = new RegExp('[' + SPACE_CHARS + ']+');
  function trimSpace(s) { return s.replace(TRIM, ''); }
  function fields(s) { return s.split(FIELDS).filter(function (w) { return w !== ''; }); }

  function utf8Length(s) {
    var n = 0;
    for (var i = 0; i < s.length; i++) {
      var c = s.charCodeAt(i);
      if (c < 0x80) n += 1;
      else if (c < 0x800) n += 2;
      else if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length) { n += 4; i++; }
      else n += 3;
    }
    return n;
  }

  function wordSet(s) {
    var set = Object.create(null);
    fields(s).forEach(function (w) { set[w] = true; });
    return set;
  }
  function has(set, w) { return set[w] === true; }
  function startsWith(s, p) { return s.lastIndexOf(p, 0) === 0; }
  function endsWith(s, p) { return s.length >= p.length && s.indexOf(p, s.length - p.length) >= 0; }

  // ---- detection ----

  var MAX_CODE_LEN = 64 << 10;

  function detectLang(text) {
    var t = trimSpace(text);
    if (t === '' || utf8Length(text) > MAX_CODE_LEN) return '';

    var f = fenced(t);
    if (f) return f.lang;
    if (startsWith(t, '#!')) return shebangLang(t.split('\n')[0]);
    if (startsWith(t, '<?php')) return 'php';

    if ((t[0] === '{' || t[0] === '[') && /[":]/.test(t) && t.indexOf('"') >= 0 && validJSON(t)) return 'json';
    if (isDiff(t)) return 'diff';
    if (isDockerfile(t)) return 'dockerfile';

    var lines = nonEmptyLines(t);
    if (isShell(lines)) return 'bash';
    if (mostlyProse(lines)) return '';

    var best = '', bestScore = 0;
    for (var i = 0; i < SCORED.length; i++) {
      var score = 0;
      SCORED[i].rules.forEach(function (r) { score += r.w * countMatches(r.re, t, 3); });
      if (score > bestScore) { best = SCORED[i].id; bestScore = score; }
    }
    if (bestScore >= 2) return refine(best, t);
    if (looksLikeCode(lines)) return 'code';
    return '';
  }

  function validJSON(t) {
    try { JSON.parse(t); return true; } catch (e) { return false; }
  }

  var FENCE_OPEN = /^```([A-Za-z0-9_+#.-]*)[ \t]*\r?\n/;

  function fenced(t) {
    var m = FENCE_OPEN.exec(t);
    if (!m || !endsWith(t, '```') || t.length < m[0].length + 3) return null;
    var body = t.slice(m[0].length, t.length - 3);
    if (endsWith(body, '\n')) body = body.slice(0, -1);
    if (body.indexOf('```') >= 0) return null;
    var name = m[1].toLowerCase();
    var lang = ALIASES[name] || '';
    if (lang === '') {
      if (Object.prototype.hasOwnProperty.call(LANGS, name)) lang = name;
      else if ((lang = detectLang(body)) === '') lang = 'code';
    }
    return { lang: lang, body: body };
  }

  var ALIASES = {
    sh: 'bash', shell: 'bash', zsh: 'bash', fish: 'bash', console: 'bash', terminal: 'bash',
    js: 'javascript', jsx: 'javascript', mjs: 'javascript', node: 'javascript',
    ts: 'typescript', tsx: 'typescript',
    py: 'python', python3: 'python',
    golang: 'go', rs: 'rust', rb: 'ruby', kt: 'kotlin', cs: 'csharp', 'c#': 'csharp',
    'c++': 'cpp', cc: 'cpp', h: 'c', hpp: 'cpp',
    yml: 'yaml', ini: 'toml', conf: 'toml', docker: 'dockerfile',
    xml: 'html', svg: 'html', htm: 'html', vue: 'html',
    ps1: 'powershell', pwsh: 'powershell', ps: 'powershell',
    patch: 'diff', psql: 'sql', mysql: 'sql', sqlite: 'sql'
  };

  function shebangLang(line) {
    if (['bash', '/sh', 'zsh', 'dash', 'ksh', ' sh'].some(function (s) { return line.indexOf(s) >= 0; })) return 'bash';
    if (line.indexOf('python') >= 0) return 'python';
    if (line.indexOf('node') >= 0 || line.indexOf('deno') >= 0 || line.indexOf('bun') >= 0) return 'javascript';
    if (line.indexOf('ruby') >= 0) return 'ruby';
    if (line.indexOf('php') >= 0) return 'php';
    if (line.indexOf('pwsh') >= 0) return 'powershell';
    return 'code';
  }

  var DIFF_HUNK = goRe('(?m)^@@ -\\d+(,\\d+)? \\+\\d+(,\\d+)? @@');
  function isDiff(t) {
    return startsWith(t, 'diff --git ') || DIFF_HUNK.test(t) || (t.indexOf('\n+++ ') >= 0 && startsWith(t, '--- '));
  }

  var DOCKER_FROM = goRe('(?m)^FROM [\\w./:@-]+( AS \\w+)?\\s*$');
  var DOCKER_OTHER = goRe('(?m)^(RUN|COPY|ADD|CMD|ENTRYPOINT|WORKDIR|ENV|EXPOSE|ARG|VOLUME|USER|LABEL) \\S');
  function isDockerfile(t) { return DOCKER_FROM.test(t) && DOCKER_OTHER.test(t); }

  // ---- shell commands ----

  // Commands that are also everyday English words; they need shell syntax
  // around them to count.
  var AMBIGUOUS_CMDS = wordSet('make cat cut find sort head tail more less man who time date test kill top touch host watch ' +
    'sleep wait read yes which where open start set type go node code history clear exit echo source export ' +
    'alias sync mount link split join paste print look tee env free file last write install service screen');

  var SHELL_CMDS = wordSet('sudo doas su apt apt-get dpkg pacman yay paru dnf yum zypper apk brew snap flatpak nix ' +
    'git gh docker docker-compose podman kubectl helm terraform ansible vagrant ' +
    'systemctl journalctl service loginctl hostnamectl timedatectl ' +
    'curl wget ssh scp sftp rsync nc ncat nmap dig nslookup ping traceroute ip ifconfig iptables ufw nft ss netstat ' +
    'cd ls ll pwd mkdir rmdir rm cp mv ln chmod chown chgrp stat du df lsblk blkid fdisk parted mkfs umount fsck dd ' +
    'grep egrep rg ag sed awk tr xargs wc uniq diff patch tar gzip gunzip zip unzip 7z xz zstd ' +
    'ps pkill killall htop btop nohup crontab uname whoami id groups passwd useradd usermod adduser ' +
    'npm npx pnpm yarn bun deno pip pip3 pipx python python3 uv poetry conda ruby gem bundle php composer ' +
    'cargo rustc rustup gcc g++ clang cmake ninja mvn gradle java javac dotnet swift xcodebuild ' +
    'nano vim vi nvim emacs code tmux zellij ' +
    'pct qm pvesh pveam pvecm pveversion zfs zpool lxc lxc-attach virsh ' +
    'ffmpeg ffprobe convert magick yt-dlp jq yq openssl gpg ssh-keygen ssh-copy-id base64 sha256sum md5sum ' +
    'wl-copy wl-paste xclip xdg-open fetch adb fastboot shopify wrangler vercel netlify fly heroku aws gcloud az');

  var SHELL_KEYWORDS = wordSet('if then else elif fi for while until do done case esac function in');

  var STOPWORDS = wordSet('is are was were be been am the a an and or but to of in on at for with that this it i you we they ' +
    'my your our their me him her them not no so if when what how why can will would should could have has had do does did ' +
    'just really very about from into than then there here also');

  var SHELL_SYNTAX = goRe('(^|\\s)(-{1,2}[A-Za-z][\\w-]*|\\|{1,2}|&&|>>?|2>&1|<<?)(\\s|=|$)|\\$[({A-Za-z_]|`[^`]+`|(^|\\s)[.~]?/[\\w.-]|\\\\$|;\\s*(do|then|done|fi)\\b');
  var SHELL_ASSIGN = goRe('^(export\\s+)?[A-Za-z_][A-Za-z0-9_]*=\\S*');
  var ENV_PREFIX = goRe('^[A-Za-z_][A-Za-z0-9_]*=\\S+$');
  var PROMPT_LINE = goRe('^(\\$|#|>|[\\w.-]+@[\\w.-]+[:~][^$#]*[$#])\\s+\\S');
  var SENTENCE_END = goRe('[.!?…:]["\')]?$');

  // 2 = certainly a shell command, 1 = plausible, 0 = no.
  function shellLine(line) {
    var l = trimSpace(line);
    if (l === '' || startsWith(l, '#')) return 1;
    var m = PROMPT_LINE.exec(l);
    if (m && (m[1] === '$' || m[1].indexOf('@') >= 0)) return 2;
    var words = fields(l);
    while (words.length > 1 && ENV_PREFIX.test(words[0])) words = words.slice(1);
    var first = words[0].replace(/^\(+/, '');
    var syntax = SHELL_SYNTAX.test(l);

    if (has(SHELL_KEYWORDS, first) || first === 'fi' || first === 'done' || first === 'esac' || first === '}') {
      if (syntax || words.length === 1 || l.indexOf('[') >= 0 || l.indexOf(';') >= 0) return 2;
      return 0;
    }
    if ((SHELL_ASSIGN.test(l) && l.split('=')[0].indexOf(' ') < 0 && words.length <= 2) ||
        (startsWith(l, 'export ') && l.indexOf('=') >= 0)) return 2;
    if (first === 'sudo' || first === 'doas') {
      if (words.length < 2) return 0;
      first = words[1];
      words = words.slice(1);
    }
    var isPath = startsWith(first, './') || startsWith(first, '~/') || (startsWith(first, '/') && first.split('/').length - 1 > 1);
    var known = has(SHELL_CMDS, first) || has(AMBIGUOUS_CMDS, first);
    if (!known && !isPath) return 0;
    if (syntax) return 2;
    if (has(AMBIGUOUS_CMDS, first) && !has(SHELL_CMDS, first)) return 0;
    if (words.length > 6 || SENTENCE_END.test(l) || l.indexOf(',') >= 0) return 0;
    for (var i = 1; i < words.length; i++) {
      if (has(STOPWORDS, words[i].toLowerCase())) return 0;
    }
    return words.length === 1 ? 1 : 2;
  }

  function isShell(lines) {
    var sure = 0, maybe = 0;
    lines.forEach(function (l) {
      var s = shellLine(l);
      if (s === 2) sure++;
      else if (s === 1) maybe++;
    });
    return sure > 0 && sure + maybe === lines.length;
  }

  // ---- prose and generic code ----

  var CODE_CHARS = goRe('[{}()\\[\\];=<>$|\\\\`]|->|::|\\w\\.\\w+\\(');
  var WORDY = goRe('^[\\p{L}\\p{N}\'’"“”.,!?:;()\\s/&%€$@#+*-]+$');

  function proseLine(line) {
    var l = trimSpace(line);
    var words = fields(l);
    if (words.length < 3) return false;
    if (!CODE_CHARS.test(l)) return true;
    return WORDY.test(l) && SENTENCE_END.test(l) && words.length >= 5 && !endsWith(l, ':');
  }

  function mostlyProse(lines) {
    var prose = lines.filter(proseLine).length;
    return prose * 3 > lines.length;
  }

  var CODE_LINE = goRe('[;{}]\\s*$|^\\s*[}\\])]|\\)\\s*\\{|=>|:=|[!=]==?|\\w\\(.*\\)|^\\s{2,}\\S|^\\t+\\S|^\\s*(//|/\\*|\\*/|#\\s?\\w+)|</?\\w+[^>]*>|^\\s*[\\w."\'-]+\\s*[:=]\\s*\\S');

  function looksLikeCode(lines) {
    if (lines.length < 3) return false;
    var hits = 0, punct = 0;
    lines.forEach(function (l) {
      if (CODE_LINE.test(l)) hits++;
      if (/[{}();=]/.test(l)) punct++;
    });
    return hits * 5 >= lines.length * 4 && punct * 2 >= lines.length;
  }

  // ---- language scoring ----

  function rules() {
    var out = [];
    for (var i = 0; i < arguments.length; i += 2) {
      out.push({ re: goRe('(?m)' + arguments[i], true), w: arguments[i + 1] });
    }
    return out;
  }

  var SCORED = [
    { id: 'python', rules: rules(
      '^\\s*def \\w+\\(.*\\)\\s*(->\\s*[\\w\\[\\], .]+)?:\\s*$', 2.0,
      '^\\s*from [\\w.]+ import [\\w*, ()]+\\s*$', 2.0,
      '^\\s*import [\\w.]+( as \\w+)?(, ?[\\w.]+)*\\s*$', 1.5,
      '^\\s*(if|elif|while|for|with|try|except|else|class) ?.*:\\s*$', 1.0,
      '\\bprint\\(|\\bself\\.|\\b(None|True|False)\\b|\\blambda \\w*:|__\\w+__|\\bf"|\\bf\'', 1.0,
      '^\\s*@\\w+(\\(.*\\))?\\s*$', 0.5) },
    { id: 'javascript', rules: rules(
      '\\b(const|let|var) [\\w${}\\[\\], ]+ = ', 1.5,
      '\\bfunction\\*? ?\\w*\\(.*\\)\\s*\\{', 2.0,
      '=>\\s*[{(\\w]', 1.0,
      '\\b(console\\.(log|error|warn)|document\\.|window\\.|require|JSON\\.(parse|stringify)|addEventListener|module\\.exports)\\b', 1.5,
      '^\\s*(import .+ from [\'"].+[\'"]|export (default |const |function |class ))', 2.0,
      '===|!==|\\bawait \\w|\\basync \\(|\\basync function', 1.0) },
    { id: 'go', rules: rules(
      '^package \\w+\\s*$', 2.0,
      '^func (\\(\\w+ \\*?\\w+\\) )?\\w+\\(.*\\)', 2.0,
      '\\w+(, \\w+)* := ', 1.0,
      '\\bfmt\\.\\w+\\(|\\berr != nil\\b|^import \\(|\\bdefer \\w|\\bgo func\\(|\\bchan \\w', 1.5) },
    { id: 'rust', rules: rules(
      '\\b(pub )?fn \\w+(<.*>)?\\(.*\\)( -> [\\w<>&\\[\\]:, \']+)?\\s*(\\{|where)', 2.0,
      '\\blet mut \\w+|\\bimpl(<.*>)? \\w+|\\buse (std|crate|super)::|\\bprintln!\\(|\\b(Some|Ok|Err)\\(|#\\[derive\\(', 1.5,
      '\\bmatch \\w+ \\{|::<|&mut \\w|&str\\b|\\bVec<', 1.0) },
    { id: 'cpp', rules: rules(
      '^\\s*#include\\s*[<"][\\w./]+[>"]', 2.0,
      '\\bint main\\s*\\(', 2.0,
      '\\bstd::\\w+|\\bcout\\s*<<|\\bcin\\s*>>|\\btemplate\\s*<|\\bnamespace \\w+|\\bnullptr\\b', 1.5,
      '\\b(printf|scanf|malloc|free|sizeof)\\(|\\b(int|char|float|double|void|unsigned|size_t) \\*?\\w+(\\[\\w*\\])?\\s*(=|;|\\()', 1.0) },
    { id: 'java', rules: rules(
      '\\bpublic (static )?(final )?(class|interface|enum|void) \\w+', 2.0,
      '\\bSystem\\.(out|err)\\.print|\\bpublic static void main\\(|^import java[x]?\\.', 2.0,
      '\\b(private|protected) (static )?(final )?[\\w<>\\[\\]]+ \\w+\\s*(=|;|\\()|\\bnew \\w+(<.*>)?\\(|@Override\\b', 1.0) },
    { id: 'csharp', rules: rules(
      '^using System(\\.[\\w.]+)?;', 2.0,
      '\\bConsole\\.(Write|Read)(Line)?\\(|\\{ get; (set; )?\\}|\\bnamespace [\\w.]+\\s*[{;]', 2.0,
      '\\b(public|private|internal) (static )?(async )?(void|string|int|bool|Task(<.*>)?|var) \\w+\\(', 1.0) },
    { id: 'swift', rules: rules(
      '^import (SwiftUI|UIKit|Foundation|Combine|AppKit)\\s*$', 2.0,
      '\\bfunc \\w+\\(.*\\)( (async )?(throws )?-> [\\w\\[\\]<>?: ]+)?\\s*\\{', 2.0,
      '\\b(guard|if) let \\w+|\\b(let|var) \\w+: \\[?\\w+\\]?\\??\\s*(=|$|\\{)|@(State|Published|MainActor|Binding|objc)\\b|\\bstruct \\w+: \\w+', 1.5) },
    { id: 'kotlin', rules: rules(
      '\\bfun \\w+\\(.*\\)(: [\\w<>?]+)?\\s*[{=]', 2.0,
      '\\b(val|var) \\w+(: [\\w<>?]+)? = |\\bprintln\\(|\\bdata class \\w+\\(|\\bwhen \\(.*\\) \\{', 1.0) },
    { id: 'ruby', rules: rules(
      '^\\s*def \\w+[?!]?(\\(.*\\))?\\s*$', 1.5,
      '^\\s*end\\s*$', 0.7,
      '\\bputs ["\'\\w]|^\\s*require [\'"]|\\bdo \\|\\w+(, \\w+)*\\||\\.each\\b|\\battr_(accessor|reader|writer) :|\\bnil\\b|=> ?:', 1.0) },
    { id: 'php', rules: rules(
      '\\$\\w+ = |\\$this->|\\becho ["\'$]', 1.0,
      '\\bfunction \\w+\\(.*\\$\\w+|->\\w+\\(|\\barray\\(|=> \\$', 1.0) },
    { id: 'powershell', rules: rules(
      '\\b(Get|Set|New|Remove|Add|Start|Stop|Restart|Invoke|Write|Import|Export|Test|Select|Where|ForEach|Out|Install|Enable|Disable)-[A-Z]\\w+', 2.0,
      '\\$env:\\w+|\\$_\\b|\\s-(eq|ne|gt|lt|like|match|contains)\\s|\\[\\w+(\\.\\w+)*\\]::', 1.5,
      '^\\s*\\$\\w+ = ', 0.7) },
    { id: 'sql', rules: rules(
      '(?i)^\\s*(select\\s+.+\\s+from|insert\\s+into|update\\s+\\w+\\s+set|delete\\s+from|create\\s+(table|index|view|database)|alter\\s+table|drop\\s+(table|index|view|database))\\b', 1.5,
      '\\b(SELECT|INSERT INTO|UPDATE|DELETE FROM|CREATE TABLE|ALTER TABLE|WHERE|GROUP BY|ORDER BY|LEFT JOIN|INNER JOIN|VALUES|PRIMARY KEY)\\b', 1.0,
      '(?i)\\b(from|where|join)\\b.*(\\*|=|;)|\\*\\s+from\\b|;\\s*$', 0.5) },
    { id: 'html', rules: rules(
      '(?i)<!doctype html|<html[ >]|</(div|span|p|a|ul|li|body|head|script|style|section|table|button|form|h[1-6])>', 2.0,
      '<\\w+(\\s+[\\w:-]+(="[^"]*")?)*\\s*/?>', 0.7,
      '\\{%-?\\s*\\w+.*?-?%\\}|\\{\\{-?\\s*[\\w.]+.*?-?\\}\\}', 1.0) },
    { id: 'css', rules: rules(
      '^\\s*[.#@:\\w][\\w\\s.#:>,*\\[\\]="\'()-]*\\{\\s*$', 1.0,
      '^\\s*[a-z-]+:\\s*[^;{}]+;\\s*$', 1.0,
      '@media |@import |@keyframes |!important|:root|:hover|\\bvar\\(--', 1.0) },
    { id: 'yaml', rules: rules(
      '^---\\s*$', 1.0,
      '^[\\w.-]+:\\s*$\\n^\\s+(- )?[\\w."\'-]+', 1.5,
      '^\\s*- [\\w.-]+: \\S', 1.0,
      '^(version|services|name|on|jobs|steps|image|ports|volumes|environment|apiVersion|kind|metadata|spec):', 1.0) },
    { id: 'toml', rules: rules(
      '^\\[{1,2}[\\w."-]+\\]{1,2}\\s*$', 1.5,
      '^[\\w.-]+ ?= ?("[^"]*"|\'[^\']*\'|\\d+|true|false|\\[)', 0.6) }
  ];

  var TS_SIGNS = goRe('(?m):\\s*(string|number|boolean|void|any|unknown|never)\\b(\\[\\])?|^\\s*(export )?(interface|type) \\w+(<.*>)?\\s*[={]|\\bas (const|string|number|any)\\b|\\w+<[A-Z]\\w*(\\[\\])?>\\(|\\b(public|private|readonly) \\w+:');
  var CPP_SIGNS = goRe('\\bstd::|\\bcout\\b|\\bcin\\b|\\btemplate\\s*<|\\bnamespace\\b|\\bclass \\w+|\\bnullptr\\b|#include\\s*<(iostream|vector|string|map|memory|algorithm)>');
  var LIQUID = goRe('\\{%-?\\s*(if|for|assign|capture|render|include|section|schema|endif|endfor|unless|case|comment|liquid|paginate|form|style)\\b');

  function refine(lang, t) {
    if (lang === 'javascript' && TS_SIGNS.test(t)) return 'typescript';
    if (lang === 'cpp' && !CPP_SIGNS.test(t)) return 'c';
    if (lang === 'html' && LIQUID.test(t)) return 'liquid';
    return lang;
  }

  function nonEmptyLines(s) {
    return s.split('\n').filter(function (l) { return trimSpace(l) !== ''; }).map(function (l) { return l.replace(/\r+$/, ''); });
  }

  // ---- highlighting ----

  function Toks() { this.list = []; }
  Toks.prototype.add = function (cls, text) {
    if (text === '') return;
    var n = this.list.length;
    if (n > 0 && this.list[n - 1][0] === cls) this.list[n - 1][1] += text;
    else this.list.push([cls, text]);
  };

  var C_FAMILY = 'if else for while do switch case default break continue return goto try catch finally throw new delete this ' +
    'class struct enum interface extends implements public private protected static final const void null true false';

  function spec(o) {
    return {
      mode: o.mode || 'generic', line: o.line || [], block: o.block || [], quotes: o.quotes || '',
      triple: !!o.triple, keywords: o.keywords ? wordSet(o.keywords) : Object.create(null),
      types: o.types ? wordSet(o.types) : Object.create(null), nocase: !!o.nocase, dollar: !!o.dollar,
      preproc: !!o.preproc, cmdlets: !!o.cmdlets
    };
  }
  var CB = ['//'], BLOCK = [['/*', '*/']];

  var LANGS = {
    code: spec({ line: ['//', '#'], block: BLOCK, quotes: '"\'', keywords: 'if else for while return function def class import from true false null nil None' }),
    bash: spec({ mode: 'shell' }),
    dockerfile: spec({ mode: 'docker' }),
    json: spec({ mode: 'json' }),
    html: spec({ mode: 'markup' }),
    liquid: spec({ mode: 'markup' }),
    css: spec({ mode: 'css' }),
    yaml: spec({ mode: 'yaml' }),
    toml: spec({ mode: 'ini' }),
    diff: spec({ mode: 'diff' }),
    python: spec({ line: ['#'], quotes: '"\'', triple: true,
      keywords: 'and as assert async await break class continue def del elif else except finally for from global if import ' +
        'in is lambda nonlocal not or pass raise return try while with yield match case True False None self',
      types: 'int str float bool list dict set tuple bytes object print len range open type isinstance enumerate zip map filter sorted sum min max any all super' }),
    javascript: spec({ line: CB, block: BLOCK, quotes: '"\'`',
      keywords: 'async await break case catch class const continue debugger default delete do else export extends finally for from ' +
        'function if import in instanceof let new of return static super switch this throw try typeof var void while with yield ' +
        'true false null undefined',
      types: 'Array Object String Number Boolean Promise Map Set Date Error JSON Math console document window' }),
    typescript: spec({ line: CB, block: BLOCK, quotes: '"\'`',
      keywords: 'abstract as async await break case catch class const continue declare default delete do else enum export extends ' +
        'finally for from function if implements import in instanceof interface is keyof let namespace new of private protected ' +
        'public readonly return satisfies static super switch this throw try type typeof var void while yield true false null undefined',
      types: 'string number boolean any unknown never object Array Object String Number Boolean Promise Map Set Date Error Record Partial console' }),
    go: spec({ line: CB, block: BLOCK, quotes: '"\'`',
      keywords: 'break case chan const continue default defer else fallthrough for func go goto if import interface map package range ' +
        'return select struct switch type var true false nil iota',
      types: 'bool byte error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any ' +
        'append cap close copy delete len make new panic print println recover' }),
    rust: spec({ line: CB, block: BLOCK, quotes: '"',
      keywords: 'as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref ' +
        'return self Self static struct super trait type unsafe use where while true false',
      types: 'bool char f32 f64 i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize str String Vec Option Result Box Some None Ok Err' }),
    c: spec({ line: CB, block: BLOCK, quotes: '"\'', preproc: true,
      keywords: 'auto break case const continue default do else enum extern for goto if inline register restrict return sizeof static ' +
        'struct switch typedef union volatile while NULL',
      types: 'char double float int long short signed unsigned void size_t bool uint8_t uint16_t uint32_t uint64_t int8_t int16_t int32_t int64_t FILE' }),
    cpp: spec({ line: CB, block: BLOCK, quotes: '"\'', preproc: true,
      keywords: C_FAMILY + ' auto constexpr explicit friend inline namespace noexcept nullptr operator override template typename ' +
        'using virtual sizeof typedef union',
      types: 'bool char double float int long short signed unsigned void size_t string vector map set unique_ptr shared_ptr std' }),
    java: spec({ line: CB, block: BLOCK, quotes: '"\'',
      keywords: C_FAMILY + ' abstract assert import instanceof native package record super synchronized throws transient var volatile',
      types: 'boolean byte char double float int long short String Integer Long Double Boolean Object List Map Set ArrayList HashMap Optional System' }),
    csharp: spec({ line: CB, block: BLOCK, quotes: '"\'',
      keywords: C_FAMILY + ' abstract as async await base foreach get in internal is namespace out override params readonly ref sealed set ' +
        'using var virtual where yield',
      types: 'bool byte char decimal double float int long object short string uint ulong ushort Task List Dictionary Console' }),
    swift: spec({ line: CB, block: BLOCK, quotes: '"',
      keywords: 'as associatedtype async await break case catch class continue default defer do else enum extension fallthrough ' +
        'fileprivate final for func guard if import in init inout internal is let mutating nil open private protocol public ' +
        'repeat return self Self some static struct subscript super switch throw throws try typealias var where while true false',
      types: 'Any Bool Character Double Float Int String Array Dictionary Set Optional Void View Text Color Data URL Date Task' }),
    kotlin: spec({ line: CB, block: BLOCK, quotes: '"\'',
      keywords: C_FAMILY + ' as by companion data fun import in internal is object open override package sealed super suspend val var when',
      types: 'Any Boolean Byte Char Double Float Int Long Short String Unit List Map Set' }),
    ruby: spec({ line: ['#'], quotes: '"\'',
      keywords: 'alias and begin break case class def defined do else elsif end ensure false for if in module next nil not or redo ' +
        'require rescue retry return self super then true undef unless until when while yield puts attr_accessor attr_reader attr_writer' }),
    php: spec({ line: ['//', '#'], block: BLOCK, quotes: '"\'', dollar: true,
      keywords: C_FAMILY + ' abstract and array as echo elseif endif endforeach endwhile fn foreach function global include isset ' +
        'namespace or print require require_once trait unset use var' }),
    powershell: spec({ line: ['#'], block: [['<#', '#>']], quotes: '"\'', dollar: true, cmdlets: true, nocase: true,
      keywords: 'begin break catch class continue do else elseif end exit filter finally for foreach function if in param process ' +
        'return switch throw trap try until while' }),
    sql: spec({ line: ['--'], block: BLOCK, quotes: '\'"', nocase: true,
      keywords: 'add all alter and as asc begin between by case check column commit constraint create cross database default delete ' +
        'desc distinct drop else end exists foreign from full group having if in index inner insert into is join key left like limit ' +
        'not null offset on or order outer primary references returning right rollback select set table then transaction union unique ' +
        'update using values view when where with true false',
      types: 'bigint boolean char date datetime decimal float int integer json jsonb numeric real serial smallint text time timestamp ' +
        'uuid varchar count sum avg min max coalesce now' })
  };

  var LABELS = {
    bash: 'Shell', code: 'Code', cpp: 'C++', csharp: 'C#', css: 'CSS', html: 'HTML', javascript: 'JavaScript',
    json: 'JSON', php: 'PHP', powershell: 'PowerShell', sql: 'SQL', toml: 'TOML', typescript: 'TypeScript',
    yaml: 'YAML', dockerfile: 'Dockerfile', c: 'C', go: 'Go', diff: 'Diff'
  };
  function langLabel(id) { return LABELS[id] || id.charAt(0).toUpperCase() + id.slice(1); }

  function highlight(text, lang) {
    var sp = LANGS[lang] || LANGS.code;
    var out = new Toks();
    var f = fenced(trimSpace(text));
    if (f) {
      var start = text.indexOf(f.body);
      if (f.body !== '' && start >= 0) {
        out.add('meta', text.slice(0, start));
        lex(f.body, sp, out);
        out.add('meta', text.slice(start + f.body.length));
        return out.list;
      }
    }
    lex(text, sp, out);
    return out.list;
  }

  function lex(s, sp, out) {
    switch (sp.mode) {
      case 'shell': return lexShell(s, out);
      case 'docker': return lexDocker(s, out);
      case 'json': return lexJSON(s, out);
      case 'markup': return lexMarkup(s, out);
      case 'css': return lexCSS(s, out);
      case 'yaml': return lexLines(s, out, yamlLine);
      case 'ini': return lexLines(s, out, iniLine);
      case 'diff': return lexLines(s, out, diffLine);
      default: return lexGeneric(s, sp, out);
    }
  }

  // Character classes, by UTF-16 code unit. Everything outside ASCII counts
  // as a letter, as it does (byte by byte) in the reference implementation.
  function code(s, i) { return s.charCodeAt(i); }
  function isIdentStart(c) { return c === 95 || (c >= 97 && c <= 122) || (c >= 65 && c <= 90) || c >= 0x80; }
  function isDigit(c) { return c >= 48 && c <= 57; }
  function isIdent(c) { return isIdentStart(c) || isDigit(c); }
  function isSpace(c) { return c === 32 || c === 9 || c === 10 || c === 13; }

  function lineEnd(s, i) {
    var j = s.indexOf('\n', i);
    return j >= 0 ? j : s.length;
  }

  function stringEnd(s, i, multiLine) {
    var q = s[i];
    for (var j = i + 1; j < s.length; j++) {
      var c = s[j];
      if (c === '\\') j++;
      else if (c === q) return j + 1;
      else if (c === '\n' && !multiLine && q !== '`') return -1;
    }
    return -1;
  }

  function numberEnd(s, i) {
    var j = i;
    while (j < s.length) {
      var c = code(s, j);
      if (isDigit(c) || (isIdentStart(c) && c < 0x80) || (c === 46 && j + 1 < s.length && isDigit(code(s, j + 1)))) j++;
      else break;
    }
    return j;
  }

  function identEnd(s, i) {
    var j = i;
    while (j < s.length && isIdent(code(s, j))) j++;
    return j;
  }

  function prefixOf(s, i, options) {
    for (var k = 0; k < options.length; k++) if (s.startsWith(options[k], i)) return options[k];
    return '';
  }

  function lexGeneric(s, sp, out) {
    var lineStart = true;
    var i = 0;
    while (i < s.length) {
      var c = code(s, i);
      var j, k;

      if (sp.preproc && lineStart && c === 35) {
        j = identEnd(s, i + 1);
        out.add('meta', s.slice(i, j));
        i = j; lineStart = false;
        continue;
      }
      var p = prefixOf(s, i, sp.line);
      if (p !== '' && !(p === '#' && i > 0 && !isSpace(code(s, i - 1))) && !(p === '//' && i > 0 && s[i - 1] === ':')) {
        j = lineEnd(s, i);
        out.add('com', s.slice(i, j));
        i = j;
        continue;
      }
      var b = null;
      for (k = 0; k < sp.block.length; k++) if (s.startsWith(sp.block[k][0], i)) { b = sp.block[k]; break; }
      if (b) {
        j = s.length;
        var close = s.indexOf(b[1], i + b[0].length);
        if (close >= 0) j = close + b[1].length;
        out.add('com', s.slice(i, j));
        i = j;
        continue;
      }
      if (sp.triple && (s.startsWith('"""', i) || s.startsWith("'''", i))) {
        j = s.length;
        var end3 = s.indexOf(s.slice(i, i + 3), i + 3);
        if (end3 >= 0) j = end3 + 3;
        out.add('str', s.slice(i, j));
        i = j; lineStart = false;
        continue;
      }
      if (sp.quotes.indexOf(s[i]) >= 0) {
        j = stringEnd(s, i, false);
        if (j > 0) {
          out.add('str', s.slice(i, j));
          i = j; lineStart = false;
          continue;
        }
      }
      if (c === 64 && i + 1 < s.length && isIdentStart(code(s, i + 1)) && (i === 0 || !isIdent(code(s, i - 1)))) {
        j = identEnd(s, i + 1);
        out.add('meta', s.slice(i, j));
        i = j; lineStart = false;
        continue;
      }
      if (sp.dollar && c === 36 && i + 1 < s.length && (isIdentStart(code(s, i + 1)) || s[i + 1] === '_')) {
        j = identEnd(s, i + 1);
        if (sp.cmdlets && j < s.length && s[j] === ':' && j + 1 < s.length && isIdentStart(code(s, j + 1))) j = identEnd(s, j + 1);
        out.add('var', s.slice(i, j));
        i = j; lineStart = false;
        continue;
      }
      if (sp.cmdlets && c === 45 && i > 0 && isSpace(code(s, i - 1)) && i + 1 < s.length && isIdentStart(code(s, i + 1))) {
        j = identEnd(s, i + 1);
        out.add('attr', s.slice(i, j));
        i = j;
        continue;
      }
      if (isDigit(c) && (i === 0 || !isIdent(code(s, i - 1)))) {
        j = numberEnd(s, i);
        out.add('num', s.slice(i, j));
        i = j; lineStart = false;
        continue;
      }
      if (isIdentStart(c)) {
        j = identEnd(s, i);
        if (sp.cmdlets) {
          while (j + 1 < s.length && s[j] === '-' && isIdentStart(code(s, j + 1))) j = identEnd(s, j + 1);
        }
        var word = s.slice(i, j);
        var look = sp.nocase ? word.toLowerCase() : word;
        if (has(sp.keywords, look)) out.add('kw', word);
        else if (has(sp.types, look)) out.add('typ', word);
        else if (sp.cmdlets && word.indexOf('-') >= 0) out.add('fn', word);
        else if (j < s.length && s[j] === '(' && !sp.nocase) out.add('fn', word);
        else out.add('', word);
        i = j; lineStart = false;
        continue;
      }
      if (c === 10) lineStart = true;
      else if (!isSpace(c)) lineStart = false;
      out.add('', s[i]);
      i++;
    }
  }

  function lexShell(s, out) {
    var cmdPos = true;
    var i = 0;
    while (i < s.length) {
      var c = s[i];
      var prev = i > 0 ? s[i - 1] : '';
      var atWordStart = i === 0 || isSpace(code(s, i - 1)) || ';|&('.indexOf(prev) >= 0;
      var lineStart = i === 0 || prev === '\n';
      var j;

      if (c === '\n') {
        out.add('', '\n');
        i++; cmdPos = true;
      } else if (isSpace(code(s, i))) {
        out.add('', c);
        i++;
      } else if (lineStart && (c === '$' || c === '>') && i + 1 < s.length && s[i + 1] === ' ') {
        out.add('meta', s.slice(i, i + 2));
        i += 2;
      } else if (c === '#' && atWordStart) {
        j = lineEnd(s, i);
        out.add('com', s.slice(i, j));
        i = j;
      } else if (c === '"' || c === "'") {
        j = stringEnd(s, i, true);
        if (j < 0) { j = i + 1; out.add('', s.slice(i, j)); } else out.add('str', s.slice(i, j));
        i = j; cmdPos = false;
      } else if (c === '$' && i + 1 < s.length && s[i + 1] === '(') {
        out.add('', '$(');
        i += 2; cmdPos = true;
      } else if (c === '$' && i + 1 < s.length && s[i + 1] === '{') {
        j = s.length;
        var close = s.indexOf('}', i);
        if (close >= 0) j = close + 1;
        out.add('var', s.slice(i, j));
        i = j; cmdPos = false;
      } else if (c === '$' && i + 1 < s.length && (isIdent(code(s, i + 1)) || '?@*#!$'.indexOf(s[i + 1]) >= 0)) {
        j = i + 2;
        if (isIdentStart(code(s, i + 1))) j = identEnd(s, i + 1);
        out.add('var', s.slice(i, j));
        i = j; cmdPos = false;
      } else if (c === '|' || c === '&' || c === ';' || c === '(' || c === '`') {
        out.add('', c);
        i++; cmdPos = true;
      } else if (c === '-' && atWordStart && i + 1 < s.length && (isIdent(code(s, i + 1)) || s[i + 1] === '-')) {
        j = i;
        while (j < s.length && !isSpace(code(s, j)) && s[j] !== '=' && ';|&)'.indexOf(s[j]) < 0) j++;
        out.add('attr', s.slice(i, j));
        i = j;
      } else {
        j = i;
        while (j < s.length && !isSpace(code(s, j)) && ';|&()"\'`$'.indexOf(s[j]) < 0) j++;
        if (j === i) j = i + 1;
        var word = s.slice(i, j);
        if (cmdPos && SHELL_ASSIGN.test(word)) {
          var eq = word.indexOf('=');
          out.add('var', word.slice(0, eq));
          out.add('', word.slice(eq));
        } else if (cmdPos && (has(SHELL_KEYWORDS, word) || word === 'export' || word === 'local' || word === 'return')) {
          out.add('kw', word);
          cmdPos = ['in', 'export', 'local', 'return', 'for', 'case', 'function'].indexOf(word) < 0;
        } else if (cmdPos && ['[', '[[', '{', '}', '!'].indexOf(word) < 0) {
          out.add('fn', word);
          cmdPos = ['sudo', 'doas', 'exec', 'time', 'nohup', 'xargs', 'env'].indexOf(word) >= 0;
        } else {
          out.add('', word);
        }
        i = j;
      }
    }
  }

  var DOCKER_INSTR = wordSet('FROM RUN CMD LABEL MAINTAINER EXPOSE ENV ADD COPY ENTRYPOINT VOLUME USER WORKDIR ARG ONBUILD STOPSIGNAL HEALTHCHECK SHELL AS');

  function lexDocker(s, out) {
    lexLines(s, out, function (line, out) {
      var trimmed = line.replace(/^[ \t]+/, '');
      var indent = line.slice(0, line.length - trimmed.length);
      var sp = trimmed.indexOf(' ');
      var word = sp >= 0 ? trimmed.slice(0, sp) : trimmed;
      var rest = sp >= 0 ? trimmed.slice(sp + 1) : '';
      if (startsWith(trimmed, '#')) { out.add('com', line); return; }
      if (!has(DOCKER_INSTR, word)) { lexShell(line, out); return; }
      out.add('', indent);
      out.add('kw', word);
      if (trimmed.length === word.length) return;
      out.add('', ' ');
      if (word === 'RUN') { lexShell(rest, out); return; }
      var sub = new Toks();
      lexShell(rest, sub);
      sub.list.forEach(function (t) { out.add(t[0] === 'fn' ? '' : t[0], t[1]); });
    });
  }

  function lexLines(s, out, each) {
    while (s.length > 0) {
      var j = s.indexOf('\n');
      if (j < 0) { each(s, out); return; }
      each(s.slice(0, j), out);
      out.add('', '\n');
      s = s.slice(j + 1);
    }
  }

  var SURROUNDED = new RegExp('^([' + SPACE_CHARS + ']*)([\\s\\S]*?)([' + SPACE_CHARS + ']*)$');

  function scalar(v, out) {
    var parts = SURROUNDED.exec(v);
    var lead = parts[1], t = parts[2], trail = parts[3];
    var comment = '';
    if (!startsWith(t, '"') && !startsWith(t, "'")) {
      var k = t.indexOf(' #');
      if (k >= 0) { comment = t.slice(k); t = t.slice(0, k); }
    }
    out.add('', lead);
    var lower = t.toLowerCase();
    if (t === '') {
      // nothing
    } else if (t[0] === '"' || t[0] === "'") out.add('str', t);
    else if (['true', 'false', 'null', 'yes', 'no', '~'].indexOf(lower) >= 0) out.add('kw', t);
    else if ((isDigit(code(t, 0)) && numberEnd(t, 0) === t.length) ||
             (t[0] === '-' && t.length > 1 && isDigit(code(t, 1)) && numberEnd(t, 1) === t.length)) out.add('num', t);
    else out.add('', t);
    out.add('com', comment);
    out.add('', trail);
  }

  function yamlLine(line, out) {
    var trimmed = line.replace(/^[ \t]+/, '');
    out.add('', line.slice(0, line.length - trimmed.length));
    if (startsWith(trimmed, '#')) { out.add('com', trimmed); return; }
    if (trimmed === '---' || trimmed === '...') { out.add('meta', trimmed); return; }
    while (startsWith(trimmed, '- ')) { out.add('', '- '); trimmed = trimmed.slice(2); }
    var k = trimmed.indexOf(':');
    if (k > 0 && (k === trimmed.length - 1 || trimmed[k + 1] === ' ') && '"\'[{'.indexOf(trimmed[0]) < 0) {
      out.add('key', trimmed.slice(0, k));
      out.add('', ':');
      scalar(trimmed.slice(k + 1), out);
      return;
    }
    scalar(trimmed, out);
  }

  function iniLine(line, out) {
    var trimmed = trimSpace(line);
    if (startsWith(trimmed, '#') || startsWith(trimmed, ';')) out.add('com', line);
    else if (startsWith(trimmed, '[')) out.add('kw', line);
    else {
      var k = line.indexOf('=');
      if (k > 0) {
        out.add('key', line.slice(0, k));
        out.add('', '=');
        scalar(line.slice(k + 1), out);
        return;
      }
      out.add('', line);
    }
  }

  function diffLine(line, out) {
    if (startsWith(line, '+++') || startsWith(line, '---') || startsWith(line, 'diff ') || startsWith(line, 'index ')) out.add('com', line);
    else if (startsWith(line, '@@')) out.add('meta', line);
    else if (startsWith(line, '+')) out.add('add', line);
    else if (startsWith(line, '-')) out.add('del', line);
    else out.add('', line);
  }

  function lexJSON(s, out) {
    var i = 0;
    while (i < s.length) {
      var c = code(s, i);
      var j;
      if (c === 34) {
        j = stringEnd(s, i, false);
        if (j < 0) j = s.length;
        var k = j;
        while (k < s.length && isSpace(code(s, k))) k++;
        out.add(k < s.length && s[k] === ':' ? 'key' : 'str', s.slice(i, j));
        i = j;
      } else if (isDigit(c) || (c === 45 && i + 1 < s.length && isDigit(code(s, i + 1)))) {
        j = numberEnd(s, i + 1);
        out.add('num', s.slice(i, j));
        i = j;
      } else if (isIdentStart(c)) {
        j = identEnd(s, i);
        out.add('kw', s.slice(i, j));
        i = j;
      } else {
        out.add('', s[i]);
        i++;
      }
    }
  }

  function templateEnd(s, k) {
    var closer = s[k + 1] === '{' ? '}}' : '%}';
    var m = s.indexOf(closer, k);
    return m >= 0 ? m + 2 : s.length;
  }

  function lexMarkup(s, out) {
    var i = 0;
    while (i < s.length) {
      var j;
      if (s.startsWith('<!--', i)) {
        j = s.length;
        var k = s.indexOf('-->', i);
        if (k >= 0) j = k + 3;
        out.add('com', s.slice(i, j));
        i = j;
      } else if (s.startsWith('{%', i) || s.startsWith('{{', i)) {
        j = templateEnd(s, i);
        lexTemplateTag(s.slice(i, j), out);
        i = j;
      } else if (s[i] === '<' && i + 1 < s.length && (isIdentStart(code(s, i + 1)) || '/!?'.indexOf(s[i + 1]) >= 0)) {
        i = lexTag(s, i, out);
      } else {
        out.add('', s[i]);
        i++;
      }
    }
  }

  function lexTag(s, i, out) {
    var j = i + 1;
    while (j < s.length && '/!?'.indexOf(s[j]) >= 0) j++;
    out.add('', s.slice(i, j));
    var k = j;
    while (k < s.length && (isIdent(code(s, k)) || s[k] === '-' || s[k] === ':')) k++;
    out.add('key', s.slice(j, k));
    while (k < s.length && s[k] !== '>') {
      var c = s[k];
      var e;
      if (c === '"' || c === "'") {
        e = stringEnd(s, k, true);
        if (e < 0) e = s.length;
        out.add('str', s.slice(k, e));
        k = e;
      } else if (s.startsWith('{{', k) || s.startsWith('{%', k)) {
        e = templateEnd(s, k);
        lexTemplateTag(s.slice(k, e), out);
        k = e;
      } else if (isIdentStart(code(s, k)) || c === '@' || c === ':') {
        e = k + 1;
        while (e < s.length && (isIdent(code(s, e)) || '-:.@'.indexOf(s[e]) >= 0)) e++;
        out.add('attr', s.slice(k, e));
        k = e;
      } else {
        out.add('', c);
        k++;
      }
    }
    if (k < s.length) {
      out.add('', '>');
      k++;
    }
    return k;
  }

  var LIQUID_SPEC = spec({ quotes: '"\'', keywords: 'if elsif else endif unless endunless for endfor in case when endcase assign capture endcapture render include ' +
    'section schema endschema comment endcomment raw endraw liquid paginate endpaginate form endform style endstyle javascript ' +
    'endjavascript break continue and or contains with as echo limit offset reversed block endblock extends set endset macro endmacro' });

  function lexTemplateTag(tag, out) {
    if (tag.length < 4) { out.add('meta', tag); return; }
    var open = tag.slice(0, 2), body = tag.slice(2, tag.length - 2), closeTag = tag.slice(tag.length - 2);
    if (!endsWith(tag, '}}') && !endsWith(tag, '%}')) { body = tag.slice(2); closeTag = ''; }
    out.add('meta', open);
    lexGeneric(body, LIQUID_SPEC, out);
    out.add('meta', closeTag);
  }

  function lexCSS(s, out) {
    var depth = 0, inValue = false;
    var i = 0;
    while (i < s.length) {
      var c = s[i];
      var cc = code(s, i);
      var j;
      if (s.startsWith('/*', i)) {
        j = s.length;
        var k = s.indexOf('*/', i + 2);
        if (k >= 0) j = k + 2;
        out.add('com', s.slice(i, j));
        i = j;
      } else if (c === '"' || c === "'") {
        j = stringEnd(s, i, false);
        if (j < 0) { j = i + 1; out.add('', s.slice(i, j)); } else out.add('str', s.slice(i, j));
        i = j;
      } else if (c === '{') {
        depth++; inValue = false; out.add('', '{'); i++;
      } else if (c === '}') {
        if (depth > 0) depth--;
        inValue = false; out.add('', '}'); i++;
      } else if (c === ';') {
        inValue = false; out.add('', ';'); i++;
      } else if (c === '@') {
        j = i + 1;
        while (j < s.length && (isIdent(code(s, j)) || s[j] === '-')) j++;
        out.add('kw', s.slice(i, j));
        i = j;
      } else if (inValue && (isDigit(cc) || (c === '.' && i + 1 < s.length && isDigit(code(s, i + 1))))) {
        j = i;
        while (j < s.length && (isIdent(code(s, j)) || s[j] === '.' || s[j] === '%')) j++;
        out.add('num', s.slice(i, j));
        i = j;
      } else if (inValue && c === '#') {
        j = identEnd(s, i + 1);
        out.add('num', s.slice(i, j));
        i = j;
      } else if (isIdentStart(cc) || (c === '-' && i + 1 < s.length && (isIdentStart(code(s, i + 1)) || s[i + 1] === '-'))) {
        j = i + 1;
        while (j < s.length && (isIdent(code(s, j)) || s[j] === '-')) j++;
        var word = s.slice(i, j);
        var k2 = j;
        while (k2 < s.length && (s[k2] === ' ' || s[k2] === '\t')) k2++;
        if (depth > 0 && !inValue && k2 < s.length && s[k2] === ':' && !isNestedSelector(s, k2)) {
          out.add('key', word);
          out.add('', s.slice(j, k2 + 1));
          inValue = true;
          j = k2 + 1;
        } else if (inValue && j < s.length && s[j] === '(') out.add('fn', word);
        else if (!inValue) out.add('typ', word);
        else out.add('', word);
        i = j;
      } else {
        out.add('', c);
        i++;
      }
    }
  }

  function isNestedSelector(s, colon) {
    for (var k = colon + 1; k < s.length; k++) {
      if (s[k] === '{') return true;
      if (s[k] === ';' || s[k] === '}') return false;
    }
    return false;
  }

  function analyze(text) {
    if (typeof text !== 'string') return null;
    var lang = detectLang(text);
    if (!lang) return null;
    return { lang: lang, label: langLabel(lang), tokens: highlight(text, lang) };
  }

  var api = { analyze: analyze, detect: detectLang, highlight: highlight, label: langLabel, languages: Object.keys(LANGS) };
  root.FetchCode = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
