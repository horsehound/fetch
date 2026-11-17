import Foundation

struct Item: Codable, Identifiable, Equatable {
    let id: String
    let kind: String          // text | image | file
    var text: String?
    var name: String?
    var mime: String?
    var size: Int
    var device: String?
    var created: Double       // unix milliseconds
    var pinned: Bool

    // Set by the hub when a text item is code: the language, its display
    // name, and the text split into [highlight class, text] pairs.
    var lang: String?
    var langLabel: String?
    var tokens: [[String]]?

    enum CodingKeys: String, CodingKey {
        case id, kind, text, name, mime, size, device, created, pinned, lang, tokens
        case langLabel = "lang_label"
    }

    var date: Date { Date(timeIntervalSince1970: created / 1000) }
    var isText: Bool { kind == "text" }
    var isImage: Bool { kind == "image" }
}

enum HubError: LocalizedError {
    case notSetUp
    case wrongToken
    case server(Int, String)
    case unreachable(String)

    var errorDescription: String? {
        switch self {
        case .notSetUp: return "Add your hub address and token in Settings."
        case .wrongToken: return "The hub rejected the token. Check it in Settings."
        case .server(let code, let message): return message.isEmpty ? "The hub answered \(code)." : message
        case .unreachable(let why): return "Can't reach the hub. \(why)"
        }
    }
}

struct HubClient {
    let base: URL
    let token: String
    let device: String

    init?(_ config: HubConfig) {
        guard config.isComplete, let url = config.baseURL else { return nil }
        base = url
        token = config.token.trimmingCharacters(in: .whitespacesAndNewlines)
        device = config.device
    }

    private func request(_ path: String, method: String = "GET", query: [URLQueryItem] = []) -> URLRequest {
        var parts = URLComponents(url: base.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty { parts.queryItems = query }
        var req = URLRequest(url: parts.url!)
        req.httpMethod = method
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        let name = device.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? "iPhone"
        req.setValue(name, forHTTPHeaderField: "X-Device")
        return req
    }

    private func check(_ response: URLResponse, _ body: Data? = nil) throws {
        guard let http = response as? HTTPURLResponse else { return }
        if http.statusCode == 401 { throw HubError.wrongToken }
        if http.statusCode >= 300 {
            let message = body.flatMap { String(data: $0.prefix(300), encoding: .utf8) } ?? ""
            throw HubError.server(http.statusCode, message.trimmingCharacters(in: .whitespacesAndNewlines))
        }
    }

    /// Turns transport failures into something readable.
    private func readable(_ error: Error) -> Error {
        if let urlError = error as? URLError, urlError.code != .cancelled {
            return HubError.unreachable(urlError.localizedDescription)
        }
        return error
    }

    private func send(_ req: URLRequest, body: Data? = nil) async throws -> Data {
        do {
            let result: (Data, URLResponse)
            if let body {
                result = try await URLSession.shared.upload(for: req, from: body)
            } else {
                result = try await URLSession.shared.data(for: req)
            }
            try check(result.1, result.0)
            return result.0
        } catch {
            throw readable(error)
        }
    }

    // MARK: reading

    func list() async throws -> [Item] {
        struct Listing: Decodable { let items: [Item] }
        let data = try await send(request("api/items"))
        return try JSONDecoder().decode(Listing.self, from: data).items
    }

    func raw(_ item: Item) async throws -> Data {
        try await send(request("api/items/\(item.id)/raw"))
    }

    /// Downloads an item into a temporary file that carries its real name,
    /// ready for the share sheet.
    func download(_ item: Item) async throws -> URL {
        let data = try await raw(item)
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(item.id, isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let fallback = item.isText ? "text.txt" : "file"
        let file = folder.appendingPathComponent(item.name ?? fallback)
        try data.write(to: file, options: .atomic)
        return file
    }

    /// A long-lived stream; every line starting with "data:" means the feed changed.
    func events() async throws -> URLSession.AsyncBytes {
        var req = request("api/events")
        req.timeoutInterval = 90 // the hub pings every 20 s, so silence means the link is dead
        do {
            let (bytes, response) = try await URLSession.shared.bytes(for: req)
            try check(response)
            return bytes
        } catch {
            throw readable(error)
        }
    }

    // MARK: writing

    func sendText(_ text: String) async throws {
        var req = request("api/items", method: "POST")
        req.setValue("text/plain; charset=utf-8", forHTTPHeaderField: "Content-Type")
        _ = try await send(req, body: Data(text.utf8))
    }

    func sendData(_ data: Data, name: String?, mime: String) async throws {
        var req = request("api/items", method: "POST", query: name.map { [URLQueryItem(name: "name", value: $0)] } ?? [])
        req.setValue(mime, forHTTPHeaderField: "Content-Type")
        _ = try await send(req, body: data)
    }

    /// Streams a file from disk, so large files never sit in memory.
    func sendFile(_ file: URL, name: String, mime: String) async throws {
        var req = request("api/items", method: "POST", query: [URLQueryItem(name: "name", value: name)])
        req.setValue(mime, forHTTPHeaderField: "Content-Type")
        do {
            let (data, response) = try await URLSession.shared.upload(for: req, fromFile: file)
            try check(response, data)
        } catch {
            throw readable(error)
        }
    }

    func delete(_ item: Item) async throws {
        _ = try await send(request("api/items/\(item.id)", method: "DELETE"))
    }

    func setPinned(_ item: Item, _ on: Bool) async throws {
        _ = try await send(request("api/items/\(item.id)/pin", method: "PUT", query: [URLQueryItem(name: "on", value: on ? "1" : "0")]))
    }

    func clearUnpinned() async throws {
        _ = try await send(request("api/items", method: "DELETE"))
    }
}
