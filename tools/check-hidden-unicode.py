#!/usr/bin/env python3
"""Fails if a tracked text file contains invisible or direction-changing
characters (zero-width spaces, line separators, bidi controls, a stray BOM)
or spaces that only look like spaces (no-break, en, ideographic...).

They are invisible in an editor and in a review, which is how a "Trojan
Source" change hides, and GitHub flags every file that has one. Where code
or test data needs such a character, write it as an escape (\\u2028).

    python3 tools/check-hidden-unicode.py
"""
import subprocess
import sys
import unicodedata

found = 0
for path in subprocess.check_output(['git', 'ls-files'], text=True).splitlines():
    try:
        text = open(path, encoding='utf-8').read()
    except (UnicodeDecodeError, IsADirectoryError, FileNotFoundError):
        continue  # binary
    for number, line in enumerate(text.split('\n'), 1):
        for ch in line:
            cat = unicodedata.category(ch)
            if cat in ('Cf', 'Zl', 'Zp') or (cat == 'Zs' and ch != ' ') or (cat == 'Cc' and ch not in '\t\r'):
                print(f'{path}:{number}: U+{ord(ch):04X} {unicodedata.name(ch, "control character")}')
                found += 1
                break
if found:
    print(f'{found} line(s) with hidden characters; write them as escapes instead.')
    sys.exit(1)
