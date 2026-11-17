import Foundation

/// One entry in the feed, decrypted on this device.
struct Item: Identifiable, Equatable {
    let id: String
    var kind: String          // text | image | file
    var text: String?
    var name: String?
    var mime: String?
    var size: Int64           // plaintext bytes
    var device: String?
    var created: Double       // unix milliseconds
    var pinned: Bool
    var salt: Data
    /// Sent with a key this device doesn't have; nothing can be shown.
    var broken = false

    var date: Date { Date(timeIntervalSince1970: created / 1000) }
    var isText: Bool { kind == "text" }
    var isImage: Bool { kind == "image" }
}

enum HubError: LocalizedError {
    case notSetUp
    case wrongToken
    case server(Int, String)
    case unreachable(String)
    case otherKey
    case alreadySetUp
    case noEncryption

    var errorDescription: String? {
        switch self {
        case .notSetUp: return "Pair this iPhone in Settings."
        case .wrongToken: return "The hub rejected the token. Pair again in Settings."
        case .server(let code, let message): return message.isEmpty ? "The hub answered \(code)." : message
        case .unreachable(let why): return "Can't reach the hub. \(why)"
        case .otherKey: return "This iPhone's key doesn't match the hub's anymore. Pair again in Settings."
        case .alreadySetUp: return "This hub already has a key. Pair from a device that has it, or start over."
        case .noEncryption: return "This hub doesn't support encryption yet. Update it first."
        }
    }
}

struct HubClient {
    let base: URL
    let token: String
    let key: Seal.Key
    let device: String

    init?(_ config: HubConfig) {
        guard config.isComplete, let url = config.baseURL, let key = config.sealKey else { return nil }
        self.init(base: url, token: config.token.trimmingCharacters(in: .whitespacesAndNewlines), key: key, device: config.device)
    }

    init(base: URL, token: String, key: Seal.Key, device: String) {
        self.base = base
        self.token = token
        self.key = key
        self.device = device
    }

    static let inlineImages: Set<String> = ["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp"]

    private func request(_ path: String, method: String = "GET", query: [URLQueryItem] = []) -> URLRequest {
        var parts = URLComponents(url: base.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty { parts.queryItems = query }
        var req = URLRequest(url: parts.url!)
        req.httpMethod = method
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
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

    // MARK: the key the devices share

    /// The fingerprint of the key the hub's devices use, or "" if none is set yet.
    func hubCheck() async throws -> String {
        let data = try await send(request("api/vault"))
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any], let check = json["check"] as? String else {
            throw HubError.noEncryption
        }
        return check
    }

    func claim() async throws {
        var req = request("api/vault", method: "PUT")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        _ = try await send(req, body: try JSONSerialization.data(withJSONObject: ["check": key.check]))
    }

    /// Deletes everything on the hub, pinned too, and its key fingerprint.
    func resetHub() async throws {
        _ = try await send(request("api/vault", method: "DELETE"))
    }

    /// Throws unless this device's key is the hub's.
    func verify() async throws {
        if try await hubCheck() != key.check { throw HubError.otherKey }
    }

    // MARK: reading

    func list() async throws -> [Item] {
        struct Sealed: Decodable {
            let id: String
            let meta: String
            let size: Int64
            let created: Double
            let pinned: Bool
        }
        struct Listing: Decodable { let items: [Sealed] }
        let data = try await send(request("api/items"))
        return try JSONDecoder().decode(Listing.self, from: data).items.map { s in
            guard let env = Data(base64Encoded: s.meta), let opened = try? Seal.openMeta(env, key: key) else {
                return Item(id: s.id, kind: "file", size: 0, created: s.created, pinned: s.pinned, salt: Data(), broken: true)
            }
            let m = opened.meta, salt = opened.salt
            var kind = ["text", "image", "file"].contains(m.kind) ? m.kind : "file"
            if kind == "image", !HubClient.inlineImages.contains(m.mime ?? "") { kind = "file" }
            let plain = s.size == 0 ? 0 : s.size - Int64((s.size + Int64(Seal.sealedChunk) - 1) / Int64(Seal.sealedChunk)) * Int64(Seal.tagSize)
            return Item(id: s.id, kind: kind, text: m.text, name: m.name, mime: m.mime, size: (m.size ?? 0) > 0 ? m.size! : plain,
                        device: m.device, created: s.created, pinned: s.pinned, salt: salt)
        }
    }

    /// The decrypted contents of a picture or file, in memory.
    func raw(_ item: Item) async throws -> Data {
        if item.isText { return Data((item.text ?? "").utf8) }
        let sealed = try await send(request("api/items/\(item.id)/raw"))
        return try Seal.openData(sealed, key: key, salt: item.salt)
    }

    /// Downloads and decrypts an item into a temporary file that carries its
    /// real name, ready for the share sheet. Large files never sit in memory.
    func download(_ item: Item) async throws -> URL {
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(item.id, isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let file = folder.appendingPathComponent(item.name ?? (item.isText ? "text.txt" : "file"))
        if item.isText {
            try Data((item.text ?? "").utf8).write(to: file, options: .atomic)
            return file
        }
        let sealed: URL
        do {
            let (tmp, response) = try await URLSession.shared.download(for: request("api/items/\(item.id)/raw"))
            try check(response)
            sealed = tmp
        } catch {
            throw readable(error)
        }
        defer { try? FileManager.default.removeItem(at: sealed) }
        try? FileManager.default.removeItem(at: file)
        try Seal.openFile(sealed, to: file, key: key, salt: item.salt)
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

    // MARK: writing (everything is encrypted here first)

    private static func stamp() -> String {
        let f = DateFormatter()
        f.dateFormat = "yyyyMMdd-HHmmss"
        f.locale = Locale(identifier: "en_US_POSIX")
        return f.string(from: Date())
    }

    private static let extensions = ["image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp", "text/plain": "txt"]

    /// Encrypts one item into a temporary upload file and sends it.
    private func upload(_ meta: Seal.Meta, source: URL?) async throws {
        var meta = meta
        meta.device = device
        let (envelope, salt) = try Seal.sealMeta(meta, key: key)
        let body = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".upload")
        defer { try? FileManager.default.removeItem(at: body) }
        try Seal.writeUpload(envelope: envelope, salt: salt, key: key, source: source, to: body)
        var req = request("api/items", method: "POST")
        req.setValue(Seal.sealedType, forHTTPHeaderField: "Content-Type")
        do {
            let (data, response) = try await URLSession.shared.upload(for: req, fromFile: body)
            try check(response, data)
        } catch {
            throw readable(error)
        }
    }

    func sendText(_ text: String) async throws {
        let bytes = Data(text.utf8)
        if bytes.count > Seal.maxTextInMeta {
            try await sendData(bytes, name: "text-\(HubClient.stamp()).txt", mime: "text/plain")
            return
        }
        try await upload(Seal.Meta(kind: "text", text: text), source: nil)
    }

    func sendData(_ data: Data, name: String?, mime: String) async throws {
        let file = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try data.write(to: file)
        defer { try? FileManager.default.removeItem(at: file) }
        let kind = HubClient.inlineImages.contains(mime) ? "image" : "file"
        let fallback = "\(kind)-\(HubClient.stamp())" + (HubClient.extensions[mime].map { "." + $0 } ?? "")
        try await sendFile(file, name: name ?? fallback, mime: mime)
    }

    /// Encrypts a file from disk as it is read, so large files never sit in memory.
    func sendFile(_ file: URL, name: String, mime: String) async throws {
        let size = (try? FileManager.default.attributesOfItem(atPath: file.path)[.size] as? NSNumber)?.int64Value ?? 0
        let kind = HubClient.inlineImages.contains(mime) ? "image" : "file"
        try await upload(Seal.Meta(kind: kind, name: name, mime: mime, size: size), source: file)
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
