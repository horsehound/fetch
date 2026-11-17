package com.horsehound.fetch

/**
 * A small, strict JSON reader with the rules of JavaScript's JSON.parse, so
 * the code detector decides "is this JSON?" exactly as the web page does. It
 * keeps its own stack, so deeply nested input can't overflow the thread's.
 *
 * Values come back as Map<String, Any?>, List<Any?>, String, Double, Boolean
 * or null.
 */
object Json {
    class Invalid(message: String) : Exception(message)

    fun isValid(text: String): Boolean = try {
        parse(text)
        true
    } catch (e: Invalid) {
        false
    }

    fun parse(text: String): Any? = Reader(text).document()

    fun quote(s: String): String {
        val out = StringBuilder(s.length + 2).append('"')
        for (c in s) {
            when {
                c == '"' -> out.append("\\\"")
                c == '\\' -> out.append("\\\\")
                c == '\n' -> out.append("\\n")
                c == '\r' -> out.append("\\r")
                c == '\t' -> out.append("\\t")
                c < ' ' -> out.append("\\u").append("%04x".format(c.code))
                else -> out.append(c)
            }
        }
        return out.append('"').toString()
    }

    private class Reader(val s: String) {
        var i = 0

        fun fail(): Nothing = throw Invalid("Not JSON (at $i).")

        fun space() {
            while (i < s.length && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r')) i++
        }

        // An open container: the list or map being filled, and for a map the key waiting for its value.
        class Open(val list: MutableList<Any?>?, val map: MutableMap<String, Any?>?) {
            var key: String? = null
        }

        fun document(): Any? {
            val stack = ArrayList<Open>()
            var result: Any? = null
            var done = false
            space()
            while (true) {
                // Read one value; containers are pushed and filled as the loop goes on.
                var value: Any?
                var haveValue = true
                if (i >= s.length) fail()
                when (s[i]) {
                    '{' -> {
                        i++
                        space()
                        if (i < s.length && s[i] == '}') {
                            i++
                            value = LinkedHashMap<String, Any?>()
                        } else {
                            val open = Open(null, LinkedHashMap())
                            open.key = key()
                            stack.add(open)
                            haveValue = false
                            value = null
                        }
                    }
                    '[' -> {
                        i++
                        space()
                        if (i < s.length && s[i] == ']') {
                            i++
                            value = ArrayList<Any?>()
                        } else {
                            stack.add(Open(ArrayList(), null))
                            haveValue = false
                            value = null
                        }
                    }
                    '"' -> value = string()
                    't' -> value = literal("true", true)
                    'f' -> value = literal("false", false)
                    'n' -> value = literal("null", null)
                    else -> value = number()
                }
                if (!haveValue) {
                    space()
                    continue
                }
                // Hand the value to its container, closing every container that ends here.
                while (true) {
                    space()
                    if (stack.isEmpty()) {
                        result = value
                        done = true
                        break
                    }
                    val top = stack.last()
                    if (top.list != null) top.list.add(value) else top.map!![top.key!!] = value
                    if (i >= s.length) fail()
                    val c = s[i]
                    if (c == ',') {
                        i++
                        space()
                        if (top.map != null) top.key = key()
                        break
                    }
                    if ((c == ']' && top.list != null) || (c == '}' && top.map != null)) {
                        i++
                        stack.removeAt(stack.size - 1)
                        value = top.list ?: top.map
                        continue
                    }
                    fail()
                }
                if (done) break
                space()
            }
            space()
            if (i != s.length) fail()
            return result
        }

        /** A map key and its colon. */
        fun key(): String {
            if (i >= s.length || s[i] != '"') fail()
            val k = string()
            space()
            if (i >= s.length || s[i] != ':') fail()
            i++
            space()
            return k
        }

        fun literal(word: String, value: Any?): Any? {
            if (!s.startsWith(word, i)) fail()
            i += word.length
            return value
        }

        fun string(): String {
            i++ // the opening quote
            val out = StringBuilder()
            while (true) {
                if (i >= s.length) fail()
                val c = s[i++]
                when {
                    c == '"' -> return out.toString()
                    c < ' ' -> fail()
                    c == '\\' -> {
                        if (i >= s.length) fail()
                        when (s[i++]) {
                            '"' -> out.append('"')
                            '\\' -> out.append('\\')
                            '/' -> out.append('/')
                            'b' -> out.append('\b')
                            'f' -> out.append('\u000c')
                            'n' -> out.append('\n')
                            'r' -> out.append('\r')
                            't' -> out.append('\t')
                            'u' -> {
                                if (i + 4 > s.length) fail()
                                var v = 0
                                repeat(4) {
                                    val d = Character.digit(s[i++], 16)
                                    if (d < 0 || s[i - 1].code > 127) fail()
                                    v = v * 16 + d
                                }
                                out.append(v.toChar())
                            }
                            else -> fail()
                        }
                    }
                    else -> out.append(c)
                }
            }
        }

        fun number(): Double {
            val start = i
            if (i < s.length && s[i] == '-') i++
            if (i >= s.length) fail()
            if (s[i] == '0') {
                i++
            } else if (s[i] in '1'..'9') {
                while (i < s.length && s[i] in '0'..'9') i++
            } else {
                fail()
            }
            if (i < s.length && s[i] == '.') {
                i++
                if (i >= s.length || s[i] !in '0'..'9') fail()
                while (i < s.length && s[i] in '0'..'9') i++
            }
            if (i < s.length && (s[i] == 'e' || s[i] == 'E')) {
                i++
                if (i < s.length && (s[i] == '+' || s[i] == '-')) i++
                if (i >= s.length || s[i] !in '0'..'9') fail()
                while (i < s.length && s[i] in '0'..'9') i++
            }
            return s.substring(start, i).toDouble()
        }
    }
}
