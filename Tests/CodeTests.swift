import XCTest

/// The iPhone runs the same fetch-code.js as the web page, through
/// JavaScriptCore; it must give the results recorded in spec/code-corpus.json.
final class CodeTests: XCTestCase {
    struct Corpus: Decodable {
        struct Detect: Decodable { let text, lang: String }
        struct Highlight: Decodable { let text, lang: String; let tokens: [[String]] }
        let plain: [String]
        let detect: [Detect]
        let highlight: [Highlight]
        let labels: [String: String]
    }

    func testMatchesTheCorpus() throws {
        let bundle = Bundle(for: CodeTests.self)
        let url = try XCTUnwrap(bundle.url(forResource: "code-corpus", withExtension: "json"))
        let corpus = try JSONDecoder().decode(Corpus.self, from: Data(contentsOf: url))
        let code = CodeHighlighter(bundle: bundle)

        for text in corpus.plain {
            XCTAssertNil(code.analyze(text), text)
        }
        for d in corpus.detect {
            XCTAssertEqual(code.analyze(d.text)?.lang ?? "", d.lang, d.text)
        }
        var checked = 0
        for h in corpus.highlight {
            guard let result = code.analyze(h.text), result.lang == h.lang else { continue }
            XCTAssertEqual(result.tokens, h.tokens, h.text)
            XCTAssertEqual(result.label, corpus.labels[h.lang])
            XCTAssertEqual(result.tokens.map { $0[1] }.joined(), h.text)
            checked += 1
        }
        XCTAssertGreaterThan(checked, 40)
    }
}
