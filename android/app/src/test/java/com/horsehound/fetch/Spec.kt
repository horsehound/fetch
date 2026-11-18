package com.horsehound.fetch

import java.io.File

/** The shared expected results in spec/, as JSON values. */
object SpecFiles {
    private fun find(name: String): File {
        // Gradle runs unit tests in the module's folder; look upwards for spec/.
        var dir: File? = File("").absoluteFile
        while (dir != null) {
            val f = File(dir, "spec/$name")
            if (f.isFile) return f
            dir = dir.parentFile
        }
        error("spec/$name not found")
    }

    fun load(name: String): Map<*, *> = Json.parse(find(name).readText()) as Map<*, *>
}

@Suppress("UNCHECKED_CAST")
fun Any?.obj() = this as Map<String, Any?>

@Suppress("UNCHECKED_CAST")
fun Any?.arr() = this as List<Any?>

fun Any?.str() = this as String
