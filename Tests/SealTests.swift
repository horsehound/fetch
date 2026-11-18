import XCTest

/// The iPhone's encryption must read and write exactly what the reference
/// implementation does, recorded in spec/vectors.json.
final class SealTests: XCTestCase {
    struct Vectors: Decodable {
        struct Item: Decodable {
            let name, salt, envelope: String
            let data: String?
            let data_size: Int
        }
        struct Corruption: Decodable {
            let name: String
            let of: Int
            let op: String
        }
        struct Invite: Decodable {
            let link, hub, code: String
        }
        let key, key_hex, check, link, hub, token: String
        let items: [Item]
        let corrupt: [Corruption]
        let invites: [Invite]
        let not_invites: [String]
    }

    var v: Vectors!
    var key: Seal.Key!

    override func setUpWithError() throws {
        let url = try XCTUnwrap(Bundle(for: SealTests.self).url(forResource: "vectors", withExtension: "json"))
        v = try JSONDecoder().decode(Vectors.self, from: Data(contentsOf: url))
        key = try Seal.Key(text: v.key)
    }

    func pattern(_ n: Int) -> Data { Data((0..<n).map { UInt8(truncatingIfNeeded: $0 * 7 + 3) }) }
    func hex(_ s: String) -> Data {
        var d = Data()
        var i = s.startIndex
        while i < s.endIndex {
            let j = s.index(i, offsetBy: 2)
            d.append(UInt8(s[i..<j], radix: 16)!)
            i = j
        }
        return d
    }

    func testKeyCheckAndLink() throws {
        XCTAssertEqual(key.data.hex, v.key_hex)
        XCTAssertEqual(key.text, v.key)
        XCTAssertEqual(key.check, v.check)
        XCTAssertEqual(Seal.Pairing(hub: v.hub, token: v.token, key: key).link, v.link)
        let p = try Seal.Pairing(link: "  " + v.link + "\n")
        XCTAssertEqual(p.hub, v.hub)
        XCTAssertEqual(p.token, v.token)
        XCTAssertEqual(p.key, key)
        XCTAssertEqual(key.fingerprint.count, 9)
        XCTAssertThrowsError(try Seal.Key(text: "short"))
        XCTAssertThrowsError(try Seal.Pairing(link: "https://example.com"))
        XCTAssertThrowsError(try Seal.Pairing(link: "fetchpair://v1?hub=a&token=b"))
        XCTAssertEqual(try Seal.Pairing(link: "fetchpair://v1?hub=192.168.1.20%3A8787&token=t&key=\(v.key)").hub, "http://192.168.1.20:8787")
        XCTAssertEqual(try Seal.Pairing(link: "fetchpair://v1?hub=fetch.example.com&token=t&key=\(v.key)").hub, "https://fetch.example.com")
    }

    func testInviteLinks() {
        for inv in v.invites {
            XCTAssertEqual(Seal.Invite(link: inv.link)?.hub, inv.hub, inv.link)
            XCTAssertEqual(Seal.Invite(link: inv.link)?.code, inv.code, inv.link)
        }
        for s in v.not_invites {
            XCTAssertNil(Seal.Invite(link: s), s)
        }
    }

    func testEnvelopesAndData() throws {
        for item in v.items {
            let env = try XCTUnwrap(Data(base64Encoded: item.envelope))
            let (meta, salt) = try Seal.openMeta(env, key: key)
            XCTAssertEqual(salt.hex, item.salt, item.name)
            // Written here, it opens to the same metadata.
            let again = try Seal.sealMeta(meta, key: key, salt: salt)
            XCTAssertEqual(try Seal.openMeta(again.envelope, key: key).meta, meta, item.name)
            // Text items carry no data.
            guard let b64 = item.data, !b64.isEmpty else { continue }
            let sealed = try XCTUnwrap(Data(base64Encoded: b64))
            XCTAssertEqual(try Seal.openData(sealed, key: key, salt: salt), pattern(item.data_size), item.name)
            XCTAssertEqual(try Seal.sealData(pattern(item.data_size), key: key, salt: salt), sealed, item.name)
            XCTAssertEqual(Seal.sealedSize(Int64(item.data_size)), Int64(sealed.count))
        }
        let text = try Seal.openMeta(Data(base64Encoded: v.items[0].envelope)!, key: key).meta
        XCTAssertEqual(text.kind, "text")
        XCTAssertEqual(text.device, "CachyOS")
        XCTAssertTrue(text.text?.contains("🎾") ?? false)
    }

    func testAlteredItemsDoNotOpen() throws {
        for c in v.corrupt {
            let item = v.items[c.of]
            var env = Data(base64Encoded: item.envelope)!
            var data = Data(base64Encoded: item.data ?? "") ?? Data()
            switch c.op {
            case "flip_envelope": env[env.count - 1] ^= 1
            case "append_zero": data.append(0)
            case "swap_first_chunks": data = data[65552..<131104] + data[0..<65552] + data[131104...]
            default: data = data.prefix(Int(c.op.dropFirst(5))!)
            }
            let salt = hex(item.salt)
            XCTAssertThrowsError(try { _ = try Seal.openMeta(env, key: self.key); if item.data != nil { _ = try Seal.openData(Data(data), key: self.key, salt: salt) } }(), c.name)
        }
        XCTAssertThrowsError(try Seal.openMeta(Data(base64Encoded: v.items[0].envelope)!, key: .new()))
    }

    func testFilesStreamThroughUploadsAndBack() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        for n in [0, 1, 65536, 65537, 200_000] {
            let plain = pattern(n)
            let source = dir.appendingPathComponent("in-\(n)")
            try plain.write(to: source)
            let (env, salt) = try Seal.sealMeta(Seal.Meta(kind: "file", name: "x"), key: key)
            let upload = dir.appendingPathComponent("up-\(n)")
            try Seal.writeUpload(envelope: env, salt: salt, key: key, source: source, to: upload)
            let body = try Data(contentsOf: upload)
            let len = Int(body[0]) << 24 | Int(body[1]) << 16 | Int(body[2]) << 8 | Int(body[3])
            XCTAssertEqual(body.subdata(in: 4..<(4 + len)), env)
            let sealed = body.subdata(in: (4 + len)..<body.count)
            XCTAssertEqual(sealed, try Seal.sealData(plain, key: key, salt: salt), "\(n) bytes")
            let sealedFile = dir.appendingPathComponent("sealed-\(n)")
            try sealed.write(to: sealedFile)
            let out = dir.appendingPathComponent("out-\(n)")
            try Seal.openFile(sealedFile, to: out, key: key, salt: salt)
            XCTAssertEqual(try Data(contentsOf: out), plain, "\(n) bytes")
        }
    }
}
