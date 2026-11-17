package main

import (
	"strings"
	"testing"
)

// What people actually send each other. None of this may be treated as code.
var plainText = []string{
	"hey can you send me the invoice from last week?",
	"Meeting moved to 3pm, room 4. Bring the laptop.",
	"https://example.com/some/long/path?with=query&and=more",
	"k9vjt-m4yjk-n4qar-f8seg",
	"+49 151 2345678",
	"Musterstraße 12, 10115 Berlin",
	"ok",
	"make sure you call me back",
	"find me a good restaurant",
	"cat is hungry again",
	"git is great and I love it",
	"docker run the container first, then tell me",
	"time to go",
	"go home",
	"node 3 is down again",
	"open the door",
	"sudo make me a sandwich",
	"python is easier than rust",
	"Name: John\nPhone: 555-1234\nEmail: john@example.com",
	"- milk\n- eggs\n- bread\n- coffee",
	"Shopping list:\n  milk\n  eggs",
	"1. unpack the printer\n2. level the bed\n3. print a test cube",
	"Select the option from the menu and click update.",
	"Please update the table and delete the old rows from it.",
	"Dear Sir or Madam,\n\nI am writing regarding order #4521 (placed on 12 March).\nPlease let me know when it ships.\n\nKind regards,\nAlex",
	"The function returns early if the list is empty; otherwise it loops.",
	"if you can, bring the cable (the long one) tomorrow",
	"price = 12.50 EUR, shipping = 4.90",
	"Wi-Fi: HomeNet\nPassword: correct horse battery staple",
	"TODO:\nfix the login bug\nwrite the README\nship it",
	"import the photos from the camera before friday",
	"for the record: I said no",
	"let me know if that works for you",
	"class starts at 9, don't be late",
	"<3 thanks!!",
	"a > b, so we go with a",
	"Tracking number: 1Z999AA10123456784",
	"use the other door; this one is locked",
	"[draft] newsletter idea: behind the scenes of the print shop",
}

var codeSamples = []struct{ want, text string }{
	// shell, the main reason this exists
	{"bash", "sudo pacman -Syu"},
	{"bash", "git status"},
	{"bash", "docker ps"},
	{"bash", "systemctl restart nginx"},
	{"bash", "ssh root@10.0.0.12"},
	{"bash", "curl -fsSL https://example.com/install.sh | sh"},
	{"bash", "$ npm install\n$ npm run build"},
	{"bash", "cd /etc/pve && ls -la"},
	{"bash", "cat /etc/os-release"},
	{"bash", "find . -name '*.png' -delete"},
	{"bash", "export PATH=$HOME/bin:$PATH"},
	{"bash", "pct enter 104"},
	{"bash", "wl-paste | fetch send"},
	{"bash", "apt update\napt install -y curl git\nsystemctl enable --now docker"},
	{"bash", "#!/bin/bash\nset -e\nfor f in *.txt; do\n  echo \"$f\"\ndone"},
	{"bash", "for i in 1 2 3; do echo $i; done"},
	{"bash", "./install.sh --prefix=/usr/local"},
	{"bash", "journalctl -u fetch-hub | grep -E 'open|token'"},
	{"bash", "# update everything\nsudo pacman -Syu\nyay -Syu"},
	{"bash", "FETCH_DOMAIN=fetch.example.com docker compose -f docker-compose.vps.yml up -d"},
	{"bash", "```bash\nmake install\n```"},

	{"python", "def greet(name):\n    print(f\"hello {name}\")\n\ngreet(\"world\")"},
	{"python", "import os\nimport sys\n\nfor path in sys.argv[1:]:\n    print(os.path.getsize(path))"},
	{"python", "from PIL import Image\nim = Image.open('a.png')\nim.save('b.jpg')"},
	{"javascript", "const items = await fetch('/api/items').then((r) => r.json());\nconsole.log(items.length);"},
	{"javascript", "function add(a, b) {\n  return a + b;\n}"},
	{"javascript", "document.querySelectorAll('.item').forEach((el) => el.remove());"},
	{"typescript", "interface Item {\n  id: string;\n  size: number;\n}\n\nconst load = async (): Promise<Item[]> => {\n  return [];\n};"},
	{"json", "{\"name\": \"fetch\", \"version\": \"1.0.0\", \"private\": true}"},
	{"json", "[\n  {\"id\": 1, \"ok\": true},\n  {\"id\": 2, \"ok\": false}\n]"},
	{"go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}"},
	{"go", "if err != nil {\n\treturn fmt.Errorf(\"open: %w\", err)\n}"},
	{"rust", "fn main() {\n    let mut total = 0;\n    println!(\"{}\", total);\n}"},
	{"c", "#include <stdio.h>\n\nint main(void) {\n    printf(\"hi\\n\");\n    return 0;\n}"},
	{"cpp", "#include <iostream>\n\nint main() {\n    std::cout << \"hi\" << std::endl;\n}"},
	{"java", "public class Main {\n    public static void main(String[] args) {\n        System.out.println(\"hi\");\n    }\n}"},
	{"swift", "import SwiftUI\n\nstruct ContentView: View {\n    @State private var count = 0\n    var body: some View { Text(\"\\(count)\") }\n}"},
	{"php", "<?php\n$name = 'world';\necho \"hello $name\";"},
	{"ruby", "def greet(name)\n  puts \"hello #{name}\"\nend"},
	{"powershell", "Get-ChildItem -Recurse | Where-Object { $_.Length -gt 1MB }"},
	{"powershell", "fetch send -t (Get-Clipboard -Raw)\nSet-ExecutionPolicy RemoteSigned"},
	{"sql", "SELECT id, name FROM users WHERE active = 1 ORDER BY name;"},
	{"sql", "select * from orders where total > 100"},
	{"html", "<div class=\"card\">\n  <p>Hello</p>\n</div>"},
	{"html", "<!DOCTYPE html>\n<html>\n<head><title>x</title></head>\n</html>"},
	{"liquid", "{% if product.available %}\n  <span class=\"price\">{{ product.price | money }}</span>\n{% endif %}"},
	{"css", ".card {\n  border-radius: 12px;\n  padding: 12px 14px;\n}"},
	{"css", ":root {\n  --bg: #111;\n}\n@media (prefers-color-scheme: dark) {\n  body { color: white; }\n}"},
	{"yaml", "services:\n  hub:\n    build: ./hub\n    ports:\n      - \"8787:8787\""},
	{"yaml", "name: Build\non:\n  push:\n    branches: [main]\njobs:\n  test:\n    runs-on: ubuntu-latest"},
	{"toml", "[package]\nname = \"fetch\"\nversion = \"0.1.0\"\n\n[dependencies]\nserde = \"1\""},
	{"dockerfile", "FROM golang:1.24-alpine AS build\nWORKDIR /src\nCOPY . .\nRUN go build -o /fetch ."},
	{"diff", "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,4 @@\n package main\n+import \"fmt\"\n-var x = 1"},
	{"code", "server {\n    listen 80;\n    server_name example.com;\n    location / {\n        proxy_pass http://localhost:8787;\n    }\n}"},
	{"python", "```python\nx = 1\n```"},
	{"code", "```\nsome thing = other;\n```"},
}

func TestPlainTextIsNotMistakenForCode(t *testing.T) {
	for _, text := range plainText {
		if lang := detectLang(text); lang != "" {
			t.Errorf("detected %q in plain text:\n%s", lang, text)
		}
	}
}

func TestCodeIsRecognisedWithItsLanguage(t *testing.T) {
	for _, c := range codeSamples {
		if got := detectLang(c.text); got != c.want {
			t.Errorf("got %q, want %q for:\n%s", got, c.want, c.text)
		}
	}
}

// Whatever the highlighter does, it must hand back exactly the text it was
// given: clients rebuild the item from the tokens.
func TestTokensAlwaysReassembleTheText(t *testing.T) {
	var inputs []string
	for _, c := range codeSamples {
		inputs = append(inputs, c.text)
	}
	inputs = append(inputs, plainText...)
	inputs = append(inputs,
		"", "'", "\"unterminated", "/* never closed", "<div", "{{ open", "{% if", "$", "$(", "${", "```\n```",
		"emoji 🎾 = \"ünïcödé\"; // ✓", "\ttabs\r\nand windows line endings\r\n", "a\\", "x = 'it''s'", "<!-- c", "@", "#", "-", "--",
	)
	for id := range langs {
		for _, text := range inputs {
			var joined strings.Builder
			for _, tok := range highlight(text, id) {
				if tok[1] == "" {
					t.Errorf("%s: empty token in %q", id, text)
				}
				joined.WriteString(tok[1])
			}
			if joined.String() != text {
				t.Fatalf("%s: tokens changed the text\n in: %q\nout: %q", id, text, joined.String())
			}
		}
	}
}

func classes(text, lang string) map[string][]string {
	out := map[string][]string{}
	for _, tok := range highlight(text, lang) {
		if tok[0] != "" {
			out[tok[0]] = append(out[tok[0]], tok[1])
		}
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestHighlightingMarksTheRightParts(t *testing.T) {
	checks := []struct {
		lang, text string
		want       map[string]string // class -> a token that must carry it
	}{
		{"bash", "sudo pacman -Syu --noconfirm # update\necho \"$HOME\" | grep -v x && FOO=1 ./run.sh $1",
			map[string]string{"fn": "pacman", "attr": "--noconfirm", "com": "# update", "str": "\"$HOME\"", "var": "FOO"}},
		{"bash", "$ git commit -m 'msg'", map[string]string{"meta": "$ ", "fn": "git", "attr": "-m", "str": "'msg'"}},
		{"bash", "for f in *.txt; do\n  echo $f\ndone", map[string]string{"kw": "for", "var": "$f", "fn": "echo"}},
		{"python", "def greet(name):  # hi\n    return f\"x\" + str(42)", map[string]string{"kw": "def", "fn": "greet", "com": "# hi", "num": "42", "typ": "str"}},
		{"javascript", "const a = `t` // note\nfoo(1.5)", map[string]string{"kw": "const", "str": "`t`", "com": "// note", "fn": "foo", "num": "1.5"}},
		{"json", "{\"name\": \"fetch\", \"n\": -3, \"ok\": true}", map[string]string{"key": "\"name\"", "str": "\"fetch\"", "num": "-3", "kw": "true"}},
		{"html", "<a href=\"/x\" class='y'>t</a><!-- c -->", map[string]string{"key": "a", "attr": "href", "str": "\"/x\"", "com": "<!-- c -->"}},
		{"liquid", "{% if a %}<b>{{ a.b }}</b>{% endif %}", map[string]string{"meta": "{%", "kw": "if", "key": "b"}},
		{"css", ".card:hover { color: #fff; margin: 0 auto; }\n@media (x) {}", map[string]string{"key": "color", "num": "#fff", "kw": "@media", "typ": "card"}},
		{"yaml", "name: fetch # c\nports:\n  - \"80:80\"\nok: true\nn: 3", map[string]string{"key": "name", "com": " # c", "str": "\"80:80\"", "kw": "true", "num": "3"}},
		{"toml", "[package]\nname = \"x\" \n# c", map[string]string{"kw": "[package]", "key": "name ", "str": "\"x\"", "com": "# c"}},
		{"diff", "@@ -1 +1 @@\n+new\n-old\n same", map[string]string{"add": "+new", "del": "-old", "meta": "@@ -1 +1 @@"}},
		{"sql", "select name from users where id = 7 -- c", map[string]string{"kw": "select", "num": "7", "com": "-- c"}},
		{"go", "func main() { x := \"s\" /* c */ }", map[string]string{"kw": "func", "fn": "main", "str": "\"s\"", "com": "/* c */"}},
		{"c", "#include <stdio.h>\nint x = 0x1F;", map[string]string{"meta": "#include", "typ": "int", "num": "0x1F"}},
		{"powershell", "Get-ChildItem -Recurse | Where-Object { $_.Name }\n$env:PATH", map[string]string{"fn": "Get-ChildItem", "attr": "-Recurse", "var": "$env:PATH"}},
		{"dockerfile", "FROM alpine\nRUN apk add --no-cache curl\nCOPY . /app", map[string]string{"kw": "FROM", "fn": "apk", "attr": "--no-cache"}},
		{"code", "see https://example.com/a // real comment", map[string]string{"com": "// real comment"}},
	}
	for _, c := range checks {
		got := classes(c.text, c.lang)
		for class, token := range c.want {
			if !has(got[class], token) {
				t.Errorf("%s: want %q marked %q in %q, got %q", c.lang, token, class, c.text, got[class])
			}
		}
	}
	// A URL's "//" is not a comment.
	if strings.Contains(strings.Join(classes("see https://example.com/a // real comment", "code")["com"], ""), "example.com") {
		t.Error("the // inside a URL was treated as a comment")
	}
}

func TestFeedCarriesLanguageAndTokensOnlyForCode(t *testing.T) {
	h := newHub(t)
	h.create("/api/items", "text/plain", []byte("see you at 5"))
	h.create("/api/items", "text/plain", []byte("sudo pacman -Syu"))
	items := h.list()
	code, plain := items[0], items[1]
	if code.Lang != "bash" || code.LangLabel != "Shell" || len(code.Tokens) == 0 {
		t.Errorf("code item: lang=%q label=%q tokens=%v", code.Lang, code.LangLabel, code.Tokens)
	}
	if plain.Lang != "" || plain.Tokens != nil {
		t.Errorf("plain item got lang=%q tokens=%v", plain.Lang, plain.Tokens)
	}
	// Analysis is for display only; it must not leak into what is stored.
	reopened, err := OpenStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range reopened.List() {
		if it.Lang != "" || it.Tokens != nil {
			t.Errorf("stored item carries analysis: %+v", it)
		}
	}
}
