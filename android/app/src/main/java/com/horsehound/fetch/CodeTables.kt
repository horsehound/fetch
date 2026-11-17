package com.horsehound.fetch

// Generated from hub/web/fetch-code.js (the reference) by
// tools/codegen/kotlin-tables.py; regenerate rather than edit. The patterns
// are in the syntax of Go's regexp package and translated by FetchCode.goRe.

internal object CodeTables {
    const val DIFF_HUNK = "(?m)^@@ -\\d+(,\\d+)? \\+\\d+(,\\d+)? @@"
    const val DOCKER_FROM = "(?m)^FROM [\\w./:@-]+( AS \\w+)?\\s*\$"
    const val DOCKER_OTHER = "(?m)^(RUN|COPY|ADD|CMD|ENTRYPOINT|WORKDIR|ENV|EXPOSE|ARG|VOLUME|USER|LABEL) \\S"
    const val SHELL_SYNTAX = "(^|\\s)(-{1,2}[A-Za-z][\\w-]*|\\|{1,2}|&&|>>?|2>&1|<<?)(\\s|=|\$)|\\\$[({A-Za-z_]|`[^`]+`|(^|\\s)[.~]?/[\\w.-]|\\\\\$|;\\s*(do|then|done|fi)\\b"
    const val SHELL_ASSIGN = "^(export\\s+)?[A-Za-z_][A-Za-z0-9_]*=\\S*"
    const val ENV_PREFIX = "^[A-Za-z_][A-Za-z0-9_]*=\\S+\$"
    const val PROMPT_LINE = "^(\\\$|#|>|[\\w.-]+@[\\w.-]+[:~][^\$#]*[\$#])\\s+\\S"
    const val SENTENCE_END = "[.!?…:][\"')]?\$"
    const val CODE_CHARS = "[{}()\\[\\];=<>\$|\\\\`]|->|::|\\w\\.\\w+\\("
    const val WORDY = "^[\\p{L}\\p{N}'’\"“”.,!?:;()\\s/&%€\$@#+*-]+\$"
    const val CODE_LINE = "[;{}]\\s*\$|^\\s*[}\\])]|\\)\\s*\\{|=>|:=|[!=]==?|\\w\\(.*\\)|^\\s{2,}\\S|^\\t+\\S|^\\s*(//|/\\*|\\*/|#\\s?\\w+)|</?\\w+[^>]*>|^\\s*[\\w.\"'-]+\\s*[:=]\\s*\\S"
    const val TS_SIGNS = "(?m):\\s*(string|number|boolean|void|any|unknown|never)\\b(\\[\\])?|^\\s*(export )?(interface|type) \\w+(<.*>)?\\s*[={]|\\bas (const|string|number|any)\\b|\\w+<[A-Z]\\w*(\\[\\])?>\\(|\\b(public|private|readonly) \\w+:"
    const val CPP_SIGNS = "\\bstd::|\\bcout\\b|\\bcin\\b|\\btemplate\\s*<|\\bnamespace\\b|\\bclass \\w+|\\bnullptr\\b|#include\\s*<(iostream|vector|string|map|memory|algorithm)>"
    const val LIQUID = "\\{%-?\\s*(if|for|assign|capture|render|include|section|schema|endif|endfor|unless|case|comment|liquid|paginate|form|style)\\b"

    val SCORED: List<Pair<String, List<Pair<String, Double>>>> = listOf(
        "python" to listOf(
            "(?m)^\\s*def \\w+\\(.*\\)\\s*(->\\s*[\\w\\[\\], .]+)?:\\s*\$" to 2.0,
            "(?m)^\\s*from [\\w.]+ import [\\w*, ()]+\\s*\$" to 2.0,
            "(?m)^\\s*import [\\w.]+( as \\w+)?(, ?[\\w.]+)*\\s*\$" to 1.5,
            "(?m)^\\s*(if|elif|while|for|with|try|except|else|class) ?.*:\\s*\$" to 1.0,
            "(?m)\\bprint\\(|\\bself\\.|\\b(None|True|False)\\b|\\blambda \\w*:|__\\w+__|\\bf\"|\\bf'" to 1.0,
            "(?m)^\\s*@\\w+(\\(.*\\))?\\s*\$" to 0.5,
        ),
        "javascript" to listOf(
            "(?m)\\b(const|let|var) [\\w\${}\\[\\], ]+ = " to 1.5,
            "(?m)\\bfunction\\*? ?\\w*\\(.*\\)\\s*\\{" to 2.0,
            "(?m)=>\\s*[{(\\w]" to 1.0,
            "(?m)\\b(console\\.(log|error|warn)|document\\.|window\\.|require|JSON\\.(parse|stringify)|addEventListener|module\\.exports)\\b" to 1.5,
            "(?m)^\\s*(import .+ from ['\"].+['\"]|export (default |const |function |class ))" to 2.0,
            "(?m)===|!==|\\bawait \\w|\\basync \\(|\\basync function" to 1.0,
        ),
        "go" to listOf(
            "(?m)^package \\w+\\s*\$" to 2.0,
            "(?m)^func (\\(\\w+ \\*?\\w+\\) )?\\w+\\(.*\\)" to 2.0,
            "(?m)\\w+(, \\w+)* := " to 1.0,
            "(?m)\\bfmt\\.\\w+\\(|\\berr != nil\\b|^import \\(|\\bdefer \\w|\\bgo func\\(|\\bchan \\w" to 1.5,
        ),
        "rust" to listOf(
            "(?m)\\b(pub )?fn \\w+(<.*>)?\\(.*\\)( -> [\\w<>&\\[\\]:, ']+)?\\s*(\\{|where)" to 2.0,
            "(?m)\\blet mut \\w+|\\bimpl(<.*>)? \\w+|\\buse (std|crate|super)::|\\bprintln!\\(|\\b(Some|Ok|Err)\\(|#\\[derive\\(" to 1.5,
            "(?m)\\bmatch \\w+ \\{|::<|&mut \\w|&str\\b|\\bVec<" to 1.0,
        ),
        "cpp" to listOf(
            "(?m)^\\s*#include\\s*[<\"][\\w./]+[>\"]" to 2.0,
            "(?m)\\bint main\\s*\\(" to 2.0,
            "(?m)\\bstd::\\w+|\\bcout\\s*<<|\\bcin\\s*>>|\\btemplate\\s*<|\\bnamespace \\w+|\\bnullptr\\b" to 1.5,
            "(?m)\\b(printf|scanf|malloc|free|sizeof)\\(|\\b(int|char|float|double|void|unsigned|size_t) \\*?\\w+(\\[\\w*\\])?\\s*(=|;|\\()" to 1.0,
        ),
        "java" to listOf(
            "(?m)\\bpublic (static )?(final )?(class|interface|enum|void) \\w+" to 2.0,
            "(?m)\\bSystem\\.(out|err)\\.print|\\bpublic static void main\\(|^import java[x]?\\." to 2.0,
            "(?m)\\b(private|protected) (static )?(final )?[\\w<>\\[\\]]+ \\w+\\s*(=|;|\\()|\\bnew \\w+(<.*>)?\\(|@Override\\b" to 1.0,
        ),
        "csharp" to listOf(
            "(?m)^using System(\\.[\\w.]+)?;" to 2.0,
            "(?m)\\bConsole\\.(Write|Read)(Line)?\\(|\\{ get; (set; )?\\}|\\bnamespace [\\w.]+\\s*[{;]" to 2.0,
            "(?m)\\b(public|private|internal) (static )?(async )?(void|string|int|bool|Task(<.*>)?|var) \\w+\\(" to 1.0,
        ),
        "swift" to listOf(
            "(?m)^import (SwiftUI|UIKit|Foundation|Combine|AppKit)\\s*\$" to 2.0,
            "(?m)\\bfunc \\w+\\(.*\\)( (async )?(throws )?-> [\\w\\[\\]<>?: ]+)?\\s*\\{" to 2.0,
            "(?m)\\b(guard|if) let \\w+|\\b(let|var) \\w+: \\[?\\w+\\]?\\??\\s*(=|\$|\\{)|@(State|Published|MainActor|Binding|objc)\\b|\\bstruct \\w+: \\w+" to 1.5,
        ),
        "kotlin" to listOf(
            "(?m)\\bfun \\w+\\(.*\\)(: [\\w<>?]+)?\\s*[{=]" to 2.0,
            "(?m)\\b(val|var) \\w+(: [\\w<>?]+)? = |\\bprintln\\(|\\bdata class \\w+\\(|\\bwhen \\(.*\\) \\{" to 1.0,
        ),
        "ruby" to listOf(
            "(?m)^\\s*def \\w+[?!]?(\\(.*\\))?\\s*\$" to 1.5,
            "(?m)^\\s*end\\s*\$" to 0.7,
            "(?m)\\bputs [\"'\\w]|^\\s*require ['\"]|\\bdo \\|\\w+(, \\w+)*\\||\\.each\\b|\\battr_(accessor|reader|writer) :|\\bnil\\b|=> ?:" to 1.0,
        ),
        "php" to listOf(
            "(?m)\\\$\\w+ = |\\\$this->|\\becho [\"'\$]" to 1.0,
            "(?m)\\bfunction \\w+\\(.*\\\$\\w+|->\\w+\\(|\\barray\\(|=> \\\$" to 1.0,
        ),
        "powershell" to listOf(
            "(?m)\\b(Get|Set|New|Remove|Add|Start|Stop|Restart|Invoke|Write|Import|Export|Test|Select|Where|ForEach|Out|Install|Enable|Disable)-[A-Z]\\w+" to 2.0,
            "(?m)\\\$env:\\w+|\\\$_\\b|\\s-(eq|ne|gt|lt|like|match|contains)\\s|\\[\\w+(\\.\\w+)*\\]::" to 1.5,
            "(?m)^\\s*\\\$\\w+ = " to 0.7,
        ),
        "sql" to listOf(
            "(?m)(?i)^\\s*(select\\s+.+\\s+from|insert\\s+into|update\\s+\\w+\\s+set|delete\\s+from|create\\s+(table|index|view|database)|alter\\s+table|drop\\s+(table|index|view|database))\\b" to 1.5,
            "(?m)\\b(SELECT|INSERT INTO|UPDATE|DELETE FROM|CREATE TABLE|ALTER TABLE|WHERE|GROUP BY|ORDER BY|LEFT JOIN|INNER JOIN|VALUES|PRIMARY KEY)\\b" to 1.0,
            "(?m)(?i)\\b(from|where|join)\\b.*(\\*|=|;)|\\*\\s+from\\b|;\\s*\$" to 0.5,
        ),
        "html" to listOf(
            "(?m)(?i)<!doctype html|<html[ >]|</(div|span|p|a|ul|li|body|head|script|style|section|table|button|form|h[1-6])>" to 2.0,
            "(?m)<\\w+(\\s+[\\w:-]+(=\"[^\"]*\")?)*\\s*/?>" to 0.7,
            "(?m)\\{%-?\\s*\\w+.*?-?%\\}|\\{\\{-?\\s*[\\w.]+.*?-?\\}\\}" to 1.0,
        ),
        "css" to listOf(
            "(?m)^\\s*[.#@:\\w][\\w\\s.#:>,*\\[\\]=\"'()-]*\\{\\s*\$" to 1.0,
            "(?m)^\\s*[a-z-]+:\\s*[^;{}]+;\\s*\$" to 1.0,
            "(?m)@media |@import |@keyframes |!important|:root|:hover|\\bvar\\(--" to 1.0,
        ),
        "yaml" to listOf(
            "(?m)^---\\s*\$" to 1.0,
            "(?m)^[\\w.-]+:\\s*\$\\n^\\s+(- )?[\\w.\"'-]+" to 1.5,
            "(?m)^\\s*- [\\w.-]+: \\S" to 1.0,
            "(?m)^(version|services|name|on|jobs|steps|image|ports|volumes|environment|apiVersion|kind|metadata|spec):" to 1.0,
        ),
        "toml" to listOf(
            "(?m)^\\[{1,2}[\\w.\"-]+\\]{1,2}\\s*\$" to 1.5,
            "(?m)^[\\w.-]+ ?= ?(\"[^\"]*\"|'[^']*'|\\d+|true|false|\\[)" to 0.6,
        ),
    )

    val AMBIGUOUS_CMDS: Set<String> = wordSet(
        "make cat cut find sort head tail more less man who time date test kill top touch host " +
        "watch sleep wait read yes which where open start set type go node code history clear exit " +
        "echo source export alias sync mount link split join paste print look tee env free file " +
        "last write install service screen",
    )

    val SHELL_CMDS: Set<String> = wordSet(
        "sudo doas su apt apt-get dpkg pacman yay paru dnf yum zypper apk brew snap flatpak nix " +
        "git gh docker docker-compose podman kubectl helm terraform ansible vagrant systemctl " +
        "journalctl service loginctl hostnamectl timedatectl curl wget ssh scp sftp rsync nc ncat " +
        "nmap dig nslookup ping traceroute ip ifconfig iptables ufw nft ss netstat cd ls ll pwd " +
        "mkdir rmdir rm cp mv ln chmod chown chgrp stat du df lsblk blkid fdisk parted mkfs umount " +
        "fsck dd grep egrep rg ag sed awk tr xargs wc uniq diff patch tar gzip gunzip zip unzip 7z " +
        "xz zstd ps pkill killall htop btop nohup crontab uname whoami id groups passwd useradd " +
        "usermod adduser npm npx pnpm yarn bun deno pip pip3 pipx python python3 uv poetry conda " +
        "ruby gem bundle php composer cargo rustc rustup gcc g++ clang cmake ninja mvn gradle java " +
        "javac dotnet swift xcodebuild nano vim vi nvim emacs code tmux zellij pct qm pvesh pveam " +
        "pvecm pveversion zfs zpool lxc lxc-attach virsh ffmpeg ffprobe convert magick yt-dlp jq " +
        "yq openssl gpg ssh-keygen ssh-copy-id base64 sha256sum md5sum wl-copy wl-paste xclip " +
        "xdg-open fetch adb fastboot shopify wrangler vercel netlify fly heroku aws gcloud az",
    )

    val SHELL_KEYWORDS: Set<String> = wordSet(
        "if then else elif fi for while until do done case esac function in",
    )

    val STOPWORDS: Set<String> = wordSet(
        "is are was were be been am the a an and or but to of in on at for with that this it i you " +
        "we they my your our their me him her them not no so if when what how why can will would " +
        "should could have has had do does did just really very about from into than then there " +
        "here also",
    )

    val DOCKER_INSTR: Set<String> = wordSet(
        "FROM RUN CMD LABEL MAINTAINER EXPOSE ENV ADD COPY ENTRYPOINT VOLUME USER WORKDIR ARG " +
        "ONBUILD STOPSIGNAL HEALTHCHECK SHELL AS",
    )

    val ALIASES: Map<String, String> = mapOf(
        "sh" to "bash",
        "shell" to "bash",
        "zsh" to "bash",
        "fish" to "bash",
        "console" to "bash",
        "terminal" to "bash",
        "js" to "javascript",
        "jsx" to "javascript",
        "mjs" to "javascript",
        "node" to "javascript",
        "ts" to "typescript",
        "tsx" to "typescript",
        "py" to "python",
        "python3" to "python",
        "golang" to "go",
        "rs" to "rust",
        "rb" to "ruby",
        "kt" to "kotlin",
        "cs" to "csharp",
        "c#" to "csharp",
        "c++" to "cpp",
        "cc" to "cpp",
        "h" to "c",
        "hpp" to "cpp",
        "yml" to "yaml",
        "ini" to "toml",
        "conf" to "toml",
        "docker" to "dockerfile",
        "xml" to "html",
        "svg" to "html",
        "htm" to "html",
        "vue" to "html",
        "ps1" to "powershell",
        "pwsh" to "powershell",
        "ps" to "powershell",
        "patch" to "diff",
        "psql" to "sql",
        "mysql" to "sql",
        "sqlite" to "sql",
    )

    val LABELS: Map<String, String> = mapOf(
        "bash" to "Shell",
        "code" to "Code",
        "cpp" to "C++",
        "csharp" to "C#",
        "css" to "CSS",
        "html" to "HTML",
        "javascript" to "JavaScript",
        "json" to "JSON",
        "php" to "PHP",
        "powershell" to "PowerShell",
        "sql" to "SQL",
        "toml" to "TOML",
        "typescript" to "TypeScript",
        "yaml" to "YAML",
        "dockerfile" to "Dockerfile",
        "c" to "C",
        "go" to "Go",
        "diff" to "Diff",
    )

    // In the reference's order, which is also FetchCode.languages.
    val LANGS: Map<String, Spec> = linkedMapOf(
        "code" to Spec(
            line = listOf("//", "#"),
            block = listOf("/*" to "*/"),
            quotes = "\"'",
            keywords = wordSet(
                "if else for while return function def class import from true false null nil None",
            ),
        ),
        "bash" to Spec(
            mode = "shell",
        ),
        "dockerfile" to Spec(
            mode = "docker",
        ),
        "json" to Spec(
            mode = "json",
        ),
        "html" to Spec(
            mode = "markup",
        ),
        "liquid" to Spec(
            mode = "markup",
        ),
        "css" to Spec(
            mode = "css",
        ),
        "yaml" to Spec(
            mode = "yaml",
        ),
        "toml" to Spec(
            mode = "ini",
        ),
        "diff" to Spec(
            mode = "diff",
        ),
        "python" to Spec(
            line = listOf("#"),
            quotes = "\"'",
            triple = true,
            keywords = wordSet(
                "and as assert async await break class continue def del elif else except finally for from " +
                "global if import in is lambda nonlocal not or pass raise return try while with yield " +
                "match case True False None self",
            ),
            types = wordSet(
                "int str float bool list dict set tuple bytes object print len range open type isinstance " +
                "enumerate zip map filter sorted sum min max any all super",
            ),
        ),
        "javascript" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"'`",
            keywords = wordSet(
                "async await break case catch class const continue debugger default delete do else export " +
                "extends finally for from function if import in instanceof let new of return static super " +
                "switch this throw try typeof var void while with yield true false null undefined",
            ),
            types = wordSet(
                "Array Object String Number Boolean Promise Map Set Date Error JSON Math console document " +
                "window",
            ),
        ),
        "typescript" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"'`",
            keywords = wordSet(
                "abstract as async await break case catch class const continue declare default delete do " +
                "else enum export extends finally for from function if implements import in instanceof " +
                "interface is keyof let namespace new of private protected public readonly return " +
                "satisfies static super switch this throw try type typeof var void while yield true false " +
                "null undefined",
            ),
            types = wordSet(
                "string number boolean any unknown never object Array Object String Number Boolean Promise " +
                "Map Set Date Error Record Partial console",
            ),
        ),
        "go" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"'`",
            keywords = wordSet(
                "break case chan const continue default defer else fallthrough for func go goto if import " +
                "interface map package range return select struct switch type var true false nil iota",
            ),
            types = wordSet(
                "bool byte error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 " +
                "uint32 uint64 uintptr any append cap close copy delete len make new panic print println " +
                "recover",
            ),
        ),
        "rust" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"",
            keywords = wordSet(
                "as async await break const continue crate dyn else enum extern fn for if impl in let loop " +
                "match mod move mut pub ref return self Self static struct super trait type unsafe use " +
                "where while true false",
            ),
            types = wordSet(
                "bool char f32 f64 i8 i16 i32 i64 i128 isize u8 u16 u32 u64 u128 usize str String Vec " +
                "Option Result Box Some None Ok Err",
            ),
        ),
        "c" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"'",
            preproc = true,
            keywords = wordSet(
                "auto break case const continue default do else enum extern for goto if inline register " +
                "restrict return sizeof static struct switch typedef union volatile while NULL",
            ),
            types = wordSet(
                "char double float int long short signed unsigned void size_t bool uint8_t uint16_t " +
                "uint32_t uint64_t int8_t int16_t int32_t int64_t FILE",
            ),
        ),
        "cpp" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"'",
            preproc = true,
            keywords = wordSet(
                "if else for while do switch case default break continue return goto try catch finally " +
                "throw new delete this class struct enum interface extends implements public private " +
                "protected static final const void null true false auto constexpr explicit friend inline " +
                "namespace noexcept nullptr operator override template typename using virtual sizeof " +
                "typedef union",
            ),
            types = wordSet(
                "bool char double float int long short signed unsigned void size_t string vector map set " +
                "unique_ptr shared_ptr std",
            ),
        ),
        "java" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"'",
            keywords = wordSet(
                "if else for while do switch case default break continue return goto try catch finally " +
                "throw new delete this class struct enum interface extends implements public private " +
                "protected static final const void null true false abstract assert import instanceof " +
                "native package record super synchronized throws transient var volatile",
            ),
            types = wordSet(
                "boolean byte char double float int long short String Integer Long Double Boolean Object " +
                "List Map Set ArrayList HashMap Optional System",
            ),
        ),
        "csharp" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"'",
            keywords = wordSet(
                "if else for while do switch case default break continue return goto try catch finally " +
                "throw new delete this class struct enum interface extends implements public private " +
                "protected static final const void null true false abstract as async await base foreach " +
                "get in internal is namespace out override params readonly ref sealed set using var " +
                "virtual where yield",
            ),
            types = wordSet(
                "bool byte char decimal double float int long object short string uint ulong ushort Task " +
                "List Dictionary Console",
            ),
        ),
        "swift" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"",
            keywords = wordSet(
                "as associatedtype async await break case catch class continue default defer do else enum " +
                "extension fallthrough fileprivate final for func guard if import in init inout internal " +
                "is let mutating nil open private protocol public repeat return self Self some static " +
                "struct subscript super switch throw throws try typealias var where while true false",
            ),
            types = wordSet(
                "Any Bool Character Double Float Int String Array Dictionary Set Optional Void View Text " +
                "Color Data URL Date Task",
            ),
        ),
        "kotlin" to Spec(
            line = listOf("//"),
            block = listOf("/*" to "*/"),
            quotes = "\"'",
            keywords = wordSet(
                "if else for while do switch case default break continue return goto try catch finally " +
                "throw new delete this class struct enum interface extends implements public private " +
                "protected static final const void null true false as by companion data fun import in " +
                "internal is object open override package sealed super suspend val var when",
            ),
            types = wordSet(
                "Any Boolean Byte Char Double Float Int Long Short String Unit List Map Set",
            ),
        ),
        "ruby" to Spec(
            line = listOf("#"),
            quotes = "\"'",
            keywords = wordSet(
                "alias and begin break case class def defined do else elsif end ensure false for if in " +
                "module next nil not or redo require rescue retry return self super then true undef unless " +
                "until when while yield puts attr_accessor attr_reader attr_writer",
            ),
        ),
        "php" to Spec(
            line = listOf("//", "#"),
            block = listOf("/*" to "*/"),
            quotes = "\"'",
            dollar = true,
            keywords = wordSet(
                "if else for while do switch case default break continue return goto try catch finally " +
                "throw new delete this class struct enum interface extends implements public private " +
                "protected static final const void null true false abstract and array as echo elseif endif " +
                "endforeach endwhile fn foreach function global include isset namespace or print require " +
                "require_once trait unset use var",
            ),
        ),
        "powershell" to Spec(
            line = listOf("#"),
            block = listOf("<#" to "#>"),
            quotes = "\"'",
            nocase = true,
            dollar = true,
            cmdlets = true,
            keywords = wordSet(
                "begin break catch class continue do else elseif end exit filter finally for foreach " +
                "function if in param process return switch throw trap try until while",
            ),
        ),
        "sql" to Spec(
            line = listOf("--"),
            block = listOf("/*" to "*/"),
            quotes = "'\"",
            nocase = true,
            keywords = wordSet(
                "add all alter and as asc begin between by case check column commit constraint create " +
                "cross database default delete desc distinct drop else end exists foreign from full group " +
                "having if in index inner insert into is join key left like limit not null offset on or " +
                "order outer primary references returning right rollback select set table then transaction " +
                "union unique update using values view when where with true false",
            ),
            types = wordSet(
                "bigint boolean char date datetime decimal float int integer json jsonb numeric real " +
                "serial smallint text time timestamp uuid varchar count sum avg min max coalesce now",
            ),
        ),
    )

    val LIQUID_SPEC = Spec(
        quotes = "\"'",
        keywords = wordSet(
            "if elsif else endif unless endunless for endfor in case when endcase assign capture " +
            "endcapture render include section schema endschema comment endcomment raw endraw liquid " +
            "paginate endpaginate form endform style endstyle javascript endjavascript break continue " +
            "and or contains with as echo limit offset reversed block endblock extends set endset " +
            "macro endmacro",
        ),
    )
}
