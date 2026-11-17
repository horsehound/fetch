'use strict';
// The code detector's expected results, recorded in spec/code-corpus.json.
// The Kotlin port in the Android app is checked against the same file.
const assert = require('node:assert/strict');
const { test } = require('node:test');
const path = require('node:path');
const FetchCode = require('../web/fetch-code.js');
const corpus = require(path.join(__dirname, '../../spec/code-corpus.json'));

test('plain text is not mistaken for code', () => {
  for (const text of corpus.plain) assert.equal(FetchCode.detect(text), '', text);
});

test('code is recognised with its language', () => {
  for (const { text, lang } of corpus.code) assert.equal(FetchCode.detect(text), lang, text);
});

test('detection matches the reference on every sample', () => {
  for (const { text, lang } of corpus.detect) assert.equal(FetchCode.detect(text), lang, text);
});

test('highlighting matches the reference token for token', () => {
  for (const { text, lang, tokens } of corpus.highlight) assert.deepEqual(FetchCode.highlight(text, lang), tokens, `${lang}: ${text}`);
});

test('mutated inputs keep their recorded results', () => {
  for (const c of corpus.fuzz) {
    assert.equal(FetchCode.detect(c.text), c.lang, JSON.stringify(c.text));
    if (c.lang) assert.deepEqual(FetchCode.highlight(c.text, c.lang), c.tokens, JSON.stringify(c.text));
    assert.deepEqual(FetchCode.highlight(c.text, 'code'), c.code, JSON.stringify(c.text));
  }
});

test('the right parts are marked', () => {
  for (const { lang, text, want } of corpus.checks) {
    const got = {};
    for (const [cls, t] of FetchCode.highlight(text, lang)) (got[cls] ||= []).push(t);
    for (const [cls, token] of Object.entries(want)) assert.ok((got[cls] || []).includes(token), `${lang}: ${token} as ${cls} in ${text}`);
  }
});

test('labels match', () => {
  for (const [id, label] of Object.entries(corpus.labels)) assert.equal(FetchCode.label(id), label);
  assert.deepEqual([...FetchCode.languages].sort(), Object.keys(corpus.labels).sort());
});

test('tokens always reassemble the exact text, in every language', () => {
  const inputs = [...corpus.plain, ...corpus.code.map((c) => c.text), ...corpus.edge];
  for (const lang of FetchCode.languages) {
    for (const text of inputs) {
      const tokens = FetchCode.highlight(text, lang);
      assert.equal(tokens.map((t) => t[1]).join(''), text, `${lang}: ${JSON.stringify(text)}`);
      assert.ok(tokens.every((t) => t[1] !== ''), `${lang}: empty token`);
    }
  }
});

test('analyze returns nothing for prose and everything for code', () => {
  assert.equal(FetchCode.analyze('see you at 5'), null);
  assert.equal(FetchCode.analyze(undefined), null);
  const a = FetchCode.analyze('sudo pacman -Syu');
  assert.equal(a.lang, 'bash');
  assert.equal(a.label, 'Shell');
  assert.equal(a.tokens.map((t) => t[1]).join(''), 'sudo pacman -Syu');
  assert.equal(FetchCode.analyze('x'.repeat(70000) + '\n;'), null, 'documents are left alone');
});
