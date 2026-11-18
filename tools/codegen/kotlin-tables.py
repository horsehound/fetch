#!/usr/bin/env python3
"""Writes android/.../CodeTables.kt from hub/web/fetch-code.js.

The Android app's code detector is a port of fetch-code.js; its patterns,
keyword lists and language table are copied from the reference by this
script, so they can't drift apart. Needs node.

    python3 tools/codegen/kotlin-tables.py
"""
import json
import os
import subprocess
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SOURCE = os.path.join(ROOT, 'hub/web/fetch-code.js')
TARGET = os.path.join(ROOT, 'android/app/src/main/java/com/horsehound/fetch/CodeTables.kt')
NAMED = ['DIFF_HUNK', 'DOCKER_FROM', 'DOCKER_OTHER', 'SHELL_SYNTAX', 'SHELL_ASSIGN', 'ENV_PREFIX', 'PROMPT_LINE',
         'SENTENCE_END', 'CODE_CHARS', 'WORDY', 'CODE_LINE', 'TS_SIGNS', 'CPP_SIGNS', 'LIQUID']


def dump():
    """Runs a copy of the reference that remembers each pattern's original source."""
    src = open(SOURCE).read()

    def sub(a, b):
        nonlocal src
        assert src.count(a) == 1, a
        src = src.replace(a, b)

    sub('function goRe(src, global) {', 'function goRe(src, global) { var __s = src;')
    sub('    return new RegExp(out, flags);\n  }', '    var __r = new RegExp(out, flags); __r.goSrc = __s; return __r;\n  }')
    sub('  var api = {', '''  function keys(o) { return Object.keys(o); }
  function dumpSpec(s) { return { mode: s.mode, line: s.line, block: s.block, quotes: s.quotes, triple: s.triple,
    keywords: keys(s.keywords), types: keys(s.types), nocase: s.nocase, dollar: s.dollar, preproc: s.preproc, cmdlets: s.cmdlets }; }
  var named = {}; %s.forEach(function (n) { named[n] = eval(n).goSrc; });
  var langs = {}; keys(LANGS).forEach(function (k) { langs[k] = dumpSpec(LANGS[k]); });
  root.__dump = { named: named,
    scored: SCORED.map(function (l) { return { id: l.id, rules: l.rules.map(function (r) { return { src: r.re.goSrc, w: r.w }; }) }; }),
    langs: langs, liquid: dumpSpec(LIQUID_SPEC),
    sets: { AMBIGUOUS_CMDS: keys(AMBIGUOUS_CMDS), SHELL_CMDS: keys(SHELL_CMDS), SHELL_KEYWORDS: keys(SHELL_KEYWORDS),
            STOPWORDS: keys(STOPWORDS), DOCKER_INSTR: keys(DOCKER_INSTR) },
    aliases: ALIASES, labels: LABELS };
  var api = {''' % json.dumps(NAMED))
    with tempfile.TemporaryDirectory() as tmp:
        path = os.path.join(tmp, 'patched.js')
        open(path, 'w').write(src)
        out = subprocess.check_output(['node', '-e', 'require(process.argv[1]); process.stdout.write(JSON.stringify(globalThis.__dump))', path])
    return json.loads(out)


def kt(s):
    return '"' + s.replace('\\', '\\\\').replace('"', '\\"').replace('$', '\\$') + '"'


def words(xs, indent):
    lines, line = [], ''
    for w in xs:
        if line and len(line) + len(w) + 1 > 90:
            lines.append(line)
            line = ''
        line += w + ' '
    lines.append(line)
    lines = [l.rstrip() + ' ' for l in lines]
    lines[-1] = lines[-1].rstrip()
    pad = ' ' * indent
    return 'wordSet(\n' + ' +\n'.join(pad + '    ' + kt(l) for l in lines) + ',\n' + pad + ')'


def spec(s, indent):
    args = []
    if s['mode'] != 'generic':
        args.append('mode = ' + kt(s['mode']))
    if s['line']:
        args.append('line = listOf(' + ', '.join(kt(x) for x in s['line']) + ')')
    if s['block']:
        args.append('block = listOf(' + ', '.join(kt(a) + ' to ' + kt(b) for a, b in s['block']) + ')')
    if s['quotes']:
        args.append('quotes = ' + kt(s['quotes']))
    for f in ['triple', 'nocase', 'dollar', 'preproc', 'cmdlets']:
        if s[f]:
            args.append(f + ' = true')
    if s['keywords']:
        args.append('keywords = ' + words(s['keywords'], indent + 4))
    if s['types']:
        args.append('types = ' + words(s['types'], indent + 4))
    if not args:
        return 'Spec()'
    pad = ' ' * (indent + 4)
    return 'Spec(\n' + ''.join(pad + a + ',\n' for a in args) + ' ' * indent + ')'


def main():
    d = dump()
    out = ['''package com.horsehound.fetch

// Generated from hub/web/fetch-code.js (the reference) by
// tools/codegen/kotlin-tables.py; regenerate rather than edit. The patterns
// are in the syntax of Go's regexp package and translated by FetchCode.goRe.

internal object CodeTables {
''']
    for n, s in d['named'].items():
        out.append(f'    const val {n} = {kt(s)}\n')
    out.append('\n    val SCORED: List<Pair<String, List<Pair<String, Double>>>> = listOf(\n')
    for l in d['scored']:
        out.append(f'        {kt(l["id"])} to listOf(\n')
        for r in l['rules']:
            out.append(f'            {kt(r["src"])} to {float(r["w"])!r},\n')
        out.append('        ),\n')
    out.append('    )\n')
    for n, xs in d['sets'].items():
        out.append(f'\n    val {n}: Set<String> = {words(xs, 4)}\n')
    out.append('\n    val ALIASES: Map<String, String> = mapOf(\n' + ''.join(f'        {kt(a)} to {kt(b)},\n' for a, b in d['aliases'].items()) + '    )\n')
    out.append('\n    val LABELS: Map<String, String> = mapOf(\n' + ''.join(f'        {kt(a)} to {kt(b)},\n' for a, b in d['labels'].items()) + '    )\n')
    out.append('\n    // In the reference\'s order, which is also FetchCode.languages.\n')
    out.append('    val LANGS: Map<String, Spec> = linkedMapOf(\n' + ''.join(f'        {kt(a)} to {spec(s, 8)},\n' for a, s in d['langs'].items()) + '    )\n')
    out.append('\n    val LIQUID_SPEC = ' + spec(d['liquid'], 4) + '\n}\n')
    open(TARGET, 'w').write(''.join(out))
    print('wrote', os.path.relpath(TARGET, ROOT))


if __name__ == '__main__':
    main()
