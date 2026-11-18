import Foundation
import JavaScriptCore

/// Recognises code in text and splits it into coloured tokens, by running the
/// same fetch-code.js the web page and the desktop app use. The hub can't do
/// this any more: it never sees the text.
final class CodeHighlighter {
    struct Result: Equatable {
        let lang: String
        let label: String
        let tokens: [[String]] // [highlight class, text]
    }

    static let shared = CodeHighlighter()

    private let queue = DispatchQueue(label: "com.horsehound.fetch.code")
    private let analyze: JSValue?

    init(bundle: Bundle = .main) {
        let context = JSContext()
        if let url = bundle.url(forResource: "fetch-code", withExtension: "js"),
           let source = try? String(contentsOf: url, encoding: .utf8) {
            context?.evaluateScript(source)
        }
        analyze = context?.objectForKeyedSubscript("FetchCode")?.objectForKeyedSubscript("analyze")
    }

    /// nil for ordinary text. JavaScriptCore isn't thread-safe, so every call
    /// goes through one queue.
    func analyze(_ text: String) -> Result? {
        queue.sync {
            guard let fn = analyze, let value = fn.call(withArguments: [text]), !value.isNull, !value.isUndefined,
                  let dict = value.toDictionary(),
                  let lang = dict["lang"] as? String, let label = dict["label"] as? String,
                  let tokens = dict["tokens"] as? [[String]]
            else { return nil }
            return Result(lang: lang, label: label, tokens: tokens)
        }
    }
}
