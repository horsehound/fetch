import CryptoKit
import Foundation
import Security

/// Fetch's end-to-end encryption, on CryptoKit. Implements
/// spec/encryption.md and is tested against spec/vectors.json, like every
/// other implementation.
enum Seal {
    static let version: UInt8 = 1
    static let chunk = 65536
    static let tagSize = 16
    static let saltSize = 16
    static let sealedChunk = chunk + tagSize
    static let sealedType = "application/x-fetch-sealed"
    /// Longer text is sent as a .txt file.
    static let maxTextInMeta = 256 * 1024

    enum Failure: LocalizedError {
        case notAKey, notALink, incompleteLink, cannotOpen, cutShort
        var errorDescription: String? {
            switch self {
            case .notAKey: return "That isn't a Fetch key."
            case .notALink: return "That isn't a Fetch pairing link (they start with fetchpair://)."
            case .incompleteLink: return "That pairing link is incomplete. Copy it again."
            case .cannotOpen: return "Can't decrypt: this was made with a different key, or it was changed."
            case .cutShort: return "Can't decrypt: the data was cut short."
            }
        }
    }

    // MARK: the key

    struct Key: Equatable {
        let data: Data

        init(data: Data) { self.data = data }

        static func new() -> Key {
            Key(data: SymmetricKey(size: .bits256).withUnsafeBytes { Data($0) })
        }

        init(text: String) throws {
            var s = text.trimmingCharacters(in: .whitespacesAndNewlines)
            while s.hasSuffix("=") { s.removeLast() }
            guard s.count == 43, s.allSatisfy({ $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-" || $0 == "_") }),
                  let d = Data(base64URL: s), d.count == 32
            else { throw Failure.notAKey }
            data = d
        }

        /// As in a pairing link: base64url without padding.
        var text: String { data.base64URL }

        /// Fingerprint of the key that the hub keeps; reveals nothing about the key.
        var check: String { Seal.hkdf(data, salt: nil, info: "fetch v1 check", count: 16).hex }

        /// The short form people compare by eye: ABCD-EF01.
        var fingerprint: String {
            let c = check.prefix(8).uppercased()
            return String(c.prefix(4)) + "-" + String(c.suffix(4))
        }
    }

    static func hkdf(_ key: Data, salt: Data?, info: String, count: Int) -> Data {
        let derived = HKDF<SHA256>.deriveKey(
            inputKeyMaterial: SymmetricKey(data: key),
            salt: salt ?? Data(count: 32),
            info: Data(info.utf8),
            outputByteCount: count
        )
        return derived.withUnsafeBytes { Data($0) }
    }

    private static func itemKey(_ key: Key, _ salt: Data, _ what: String) -> SymmetricKey {
        SymmetricKey(data: hkdf(key.data, salt: salt, info: "fetch v1 \(what)", count: 32))
    }

    private static func gcmSeal(_ key: SymmetricKey, nonce: Data, _ plain: Data) throws -> Data {
        let box = try AES.GCM.seal(plain, using: key, nonce: AES.GCM.Nonce(data: nonce))
        return box.ciphertext + box.tag
    }

    private static func gcmOpen(_ key: SymmetricKey, nonce: Data, _ sealed: Data) throws -> Data {
        guard sealed.count >= tagSize else { throw Failure.cannotOpen }
        do {
            let box = try AES.GCM.SealedBox(
                nonce: AES.GCM.Nonce(data: nonce),
                ciphertext: sealed.prefix(sealed.count - tagSize),
                tag: sealed.suffix(tagSize)
            )
            return try AES.GCM.open(box, using: key)
        } catch {
            throw Failure.cannotOpen
        }
    }

    // MARK: metadata

    struct Meta: Equatable {
        var kind: String          // text | image | file
        var device: String?
        var text: String?
        var name: String?
        var mime: String?
        var size: Int64?
    }

    static func sealMeta(_ meta: Meta, key: Key, salt: Data? = nil) throws -> (envelope: Data, salt: Data) {
        let salt = salt ?? randomBytes(saltSize)
        var json: [String: Any] = ["v": Int(version), "kind": meta.kind]
        if let v = meta.device, !v.isEmpty { json["device"] = v }
        if let v = meta.text, !v.isEmpty { json["text"] = v }
        if let v = meta.name, !v.isEmpty { json["name"] = v }
        if let v = meta.mime, !v.isEmpty { json["mime"] = v }
        if let v = meta.size, v > 0 { json["size"] = v }
        let plain = try JSONSerialization.data(withJSONObject: json)
        let sealed = try gcmSeal(itemKey(key, salt, "meta"), nonce: Data(count: 12), plain)
        return (Data([version]) + salt + sealed, salt)
    }

    static func openMeta(_ envelope: Data, key: Key) throws -> (meta: Meta, salt: Data) {
        let env = Data(envelope)
        guard env.count >= 1 + saltSize + tagSize, env[env.startIndex] == version else { throw Failure.cannotOpen }
        let salt = env.subdata(in: 1..<(1 + saltSize))
        let plain = try gcmOpen(itemKey(key, salt, "meta"), nonce: Data(count: 12), env.subdata(in: (1 + saltSize)..<env.count))
        guard let json = try? JSONSerialization.jsonObject(with: plain) as? [String: Any] else { throw Failure.cannotOpen }
        let str = { (k: String) in json[k] as? String }
        let size = (json["size"] as? NSNumber)?.int64Value
        return (Meta(kind: str("kind") ?? "file", device: str("device"), text: str("text"), name: str("name"), mime: str("mime"), size: size), salt)
    }

    // MARK: data

    static func nonce(_ i: UInt64, last: Bool) -> Data {
        var n = Data(count: 12)
        for b in 0..<8 { n[10 - b] = UInt8((i >> (8 * UInt64(b))) & 0xff) }
        n[11] = last ? 1 : 0
        return n
    }

    static func sealedSize(_ plain: Int64) -> Int64 {
        plain + Int64(max(1, (plain + Int64(chunk) - 1) / Int64(chunk))) * Int64(tagSize)
    }

    /// Encrypts chunk by chunk: full chunks of `chunk` bytes, then a last one.
    final class DataSealer {
        private let key: SymmetricKey
        private var index: UInt64 = 0
        init(key: Key, salt: Data) { self.key = Seal.itemKey(key, salt, "data") }
        func seal(_ chunk: Data, last: Bool) throws -> Data {
            defer { index += 1 }
            return try Seal.gcmSeal(key, nonce: Seal.nonce(index, last: last), chunk)
        }
    }

    /// Decrypts a stream: push what arrives, end when it is complete. A full
    /// chunk is only known not to be the last once more data follows it.
    final class DataOpener {
        private let key: SymmetricKey
        private var index: UInt64 = 0
        private var pending = Data()
        init(key: Key, salt: Data) { self.key = Seal.itemKey(key, salt, "data") }

        func push(_ bytes: Data) throws -> Data {
            pending.append(bytes)
            var out = Data()
            while pending.count > Seal.sealedChunk {
                out.append(try open(pending.prefix(Seal.sealedChunk), last: false))
                pending = Data(pending.dropFirst(Seal.sealedChunk))
            }
            return out
        }

        func end() throws -> Data {
            guard pending.count >= Seal.tagSize else { throw Failure.cutShort }
            return try open(pending, last: true)
        }

        private func open(_ chunk: Data, last: Bool) throws -> Data {
            defer { index += 1 }
            return try Seal.gcmOpen(key, nonce: Seal.nonce(index, last: last), Data(chunk))
        }
    }

    static func sealData(_ plain: Data, key: Key, salt: Data) throws -> Data {
        let sealer = DataSealer(key: key, salt: salt)
        let n = max(1, (plain.count + chunk - 1) / chunk)
        var out = Data(capacity: plain.count + n * tagSize)
        for i in 0..<n {
            let start = plain.startIndex + i * chunk
            let end = min(start + chunk, plain.endIndex)
            out.append(try sealer.seal(plain.subdata(in: start..<end), last: i == n - 1))
        }
        return out
    }

    static func openData(_ sealed: Data, key: Key, salt: Data) throws -> Data {
        let opener = DataOpener(key: key, salt: salt)
        var out = try opener.push(sealed)
        out.append(try opener.end())
        return out
    }

    /// Writes an upload body to a temporary file: the envelope length (4 bytes,
    /// big-endian), the envelope, then the data, encrypted as it is read from
    /// `source` so large files never sit in memory.
    static func writeUpload(envelope: Data, salt: Data, key: Key, source: URL?, to destination: URL) throws {
        FileManager.default.createFile(atPath: destination.path, contents: nil)
        let out = try FileHandle(forWritingTo: destination)
        defer { try? out.close() }
        var head = Data(count: 4)
        let n = UInt32(envelope.count)
        head[0] = UInt8(n >> 24); head[1] = UInt8((n >> 16) & 0xff); head[2] = UInt8((n >> 8) & 0xff); head[3] = UInt8(n & 0xff)
        try out.write(contentsOf: head + envelope)
        guard let source else { return }
        let input = try FileHandle(forReadingFrom: source)
        defer { try? input.close() }
        let sealer = DataSealer(key: key, salt: salt)
        var current = try input.read(upToCount: chunk) ?? Data()
        while true {
            // A full chunk is the last one only if nothing follows it.
            var next = Data()
            if current.count == chunk {
                next = try input.read(upToCount: chunk) ?? Data()
            }
            let last = next.isEmpty
            try out.write(contentsOf: try sealer.seal(current, last: last))
            if last { break }
            current = next
        }
    }

    /// Decrypts an encrypted data file into another file, chunk by chunk.
    static func openFile(_ source: URL, to destination: URL, key: Key, salt: Data) throws {
        FileManager.default.createFile(atPath: destination.path, contents: nil)
        let out = try FileHandle(forWritingTo: destination)
        defer { try? out.close() }
        let input = try FileHandle(forReadingFrom: source)
        defer { try? input.close() }
        let opener = DataOpener(key: key, salt: salt)
        while let piece = try input.read(upToCount: 1 << 20), !piece.isEmpty {
            try out.write(contentsOf: try opener.push(piece))
        }
        try out.write(contentsOf: try opener.end())
    }

    static func randomBytes(_ n: Int) -> Data {
        var bytes = [UInt8](repeating: 0, count: n)
        _ = SecRandomCopyBytes(kSecRandomDefault, n, &bytes)
        return Data(bytes)
    }

    // MARK: pairing links

    struct Pairing: Equatable {
        var hub: String
        var token: String
        var key: Key

        var link: String {
            "fetchpair://v1?hub=\(Seal.queryEscape(hub))&key=\(key.text)&token=\(Seal.queryEscape(token))"
        }

        init(hub: String, token: String, key: Key) {
            self.hub = hub
            self.token = token
            self.key = key
        }

        init(link: String) throws {
            let s = link.trimmingCharacters(in: .whitespacesAndNewlines)
            guard s.lowercased().hasPrefix("fetchpair://v1?"), let comps = URLComponents(string: s) else { throw Failure.notALink }
            var q: [String: String] = [:]
            for item in comps.queryItems ?? [] { q[item.name] = item.value?.replacingOccurrences(of: "+", with: " ") }
            guard let hub = Seal.normalizeURL(q["hub"] ?? ""),
                  let token = q["token"]?.trimmingCharacters(in: .whitespaces), !token.isEmpty,
                  let key = try? Key(text: q["key"] ?? "")
            else { throw Failure.incompleteLink }
            self.hub = hub
            self.token = token
            self.key = key
        }
    }

    static func queryEscape(_ s: String) -> String {
        var allowed = CharacterSet.alphanumerics
        allowed.insert(charactersIn: "-._~")
        return s.addingPercentEncoding(withAllowedCharacters: allowed) ?? s
    }

    /// http for an address on a home network ("192.168.1.20:8787",
    /// "proxmox.lan"), https for a domain. Every client does the same.
    static func normalizeURL(_ input: String) -> String? {
        var text = input.trimmingCharacters(in: .whitespacesAndNewlines)
        while text.hasSuffix("/") { text.removeLast() }
        guard !text.isEmpty else { return nil }
        if !text.contains("://") {
            let host = text.split(separator: "/").first.map(String.init) ?? text
            let isLocal = host.contains(":") || !host.contains(".")
                || host.hasSuffix(".local") || host.hasSuffix(".lan")
                || host.allSatisfy { $0.isNumber || $0 == "." }
            text = (isLocal ? "http://" : "https://") + text
        }
        guard let url = URL(string: text), let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let host = url.host, !host.isEmpty
        else { return nil }
        return "\(scheme)://\(host)" + (url.port.map { ":\($0)" } ?? "")
    }
}

extension Data {
    init?(base64URL: String) {
        var s = base64URL.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while s.count % 4 != 0 { s += "=" }
        self.init(base64Encoded: s)
    }

    var base64URL: String {
        base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    var hex: String { map { String(format: "%02x", $0) }.joined() }
}
