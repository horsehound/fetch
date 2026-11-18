import ImageIO
import Photos
import SwiftUI
import UIKit
import UniformTypeIdentifiers

struct SharedFile: Identifiable {
    let id = UUID()
    let url: URL
}

enum Connection {
    case connecting, connected, offline
}

@MainActor
final class FeedModel: ObservableObject {
    @Published private(set) var items: [Item] = []
    @Published private(set) var config = HubConfig.load()
    @Published private(set) var problem: String?
    /// Whether the live connection to the hub is up, for the status pill.
    @Published private(set) var connection: Connection = .connecting
    /// Uploads in flight.
    @Published private(set) var sending = 0
    @Published var toast: String?
    @Published var sharing: SharedFile?
    /// A pairing link opened from outside (the Camera app, a tapped link),
    /// waiting for the person to confirm it.
    @Published var pendingPairing: Seal.Pairing?

    private var liveTask: Task<Void, Never>?
    private var toastTask: Task<Void, Never>?
    private let thumbnails = NSCache<NSString, UIImage>()
    private var codeCache: [String: CodeHighlighter.Result?] = [:]

    var client: HubClient? { HubClient(config) }

    /// Pinned items first, then newest first (the order the hub sends).
    var ordered: [Item] { items.filter(\.pinned) + items.filter { !$0.pinned } }

    // MARK: connection

    func apply(_ new: HubConfig) {
        new.save()
        config = new
        items = []
        codeCache = [:]
        thumbnails.removeAllObjects()
        problem = nil
        start()
    }

    // MARK: pairing

    /// A fetchpair:// link arrived from outside; ask before using it, because
    /// it decides where everything you send goes.
    func received(_ url: URL) {
        guard let pairing = try? Seal.Pairing(link: url.absoluteString) else {
            flash("That pairing link is incomplete")
            return
        }
        if pairing == config.pairing {
            flash("Already paired with this hub")
            return
        }
        pendingPairing = pairing
    }

    /// Makes a space of your own with an invite link; this iPhone is its
    /// first device.
    func join(_ invite: Seal.Invite) async throws {
        try await setUp(server: invite.hub, token: invite.code, startOver: false)
        flash("Your space is ready")
    }

    /// Joins with a pairing link. Returns an error to show, or nil.
    func pair(_ pairing: Seal.Pairing) async -> String? {
        guard let base = URL(string: pairing.hub) else { return Seal.Failure.incompleteLink.localizedDescription }
        let client = HubClient(base: base, token: pairing.token, key: pairing.key, device: config.device)
        do {
            let check = try await client.hubCheck()
            if check.isEmpty {
                try await client.claim() // the hub was reset since the link was made
            } else if check != pairing.key.check {
                return "This link's key isn't the one the hub uses now. Make a fresh link on a device that works."
            }
        } catch HubError.wrongToken {
            return "The hub doesn't accept this link's token."
        } catch {
            return error.localizedDescription
        }
        var next = config
        next.server = pairing.hub
        next.token = pairing.token
        next.key = pairing.key.text
        apply(next)
        flash("Paired")
        return nil
    }

    /// The first device of a feed: creates the key. The code is the hub's
    /// token (your own hub) or an invite code (someone else's); a public hub
    /// needs neither. With startOver, a feed that already has a key is wiped
    /// first.
    func setUp(server: String, token code: String, startOver: Bool) async throws {
        guard let hub = Seal.normalizeURL(server), let base = URL(string: hub) else {
            throw HubError.server(0, "That address doesn't look right.")
        }
        let key = Seal.Key.new()
        var token = code.trimmingCharacters(in: .whitespacesAndNewlines)
        if token.isEmpty {
            token = try await HubClient.join(base, code: "")
        } else if startOver {
            try await HubClient(base: base, token: token, key: key, device: config.device).resetHub()
        } else {
            do {
                let existing = try await HubClient(base: base, token: token, key: key, device: config.device).hubCheck()
                if !existing.isEmpty { throw HubError.alreadySetUp }
            } catch HubError.wrongToken {
                // Not a token here: maybe an invite code.
                do {
                    token = try await HubClient.join(base, code: token)
                } catch HubError.server(403, _) {
                    throw HubError.server(403, "The hub didn't accept that as its token or as an invite code.")
                }
            }
        }
        let client = HubClient(base: base, token: token, key: key, device: config.device)
        try await client.claim()
        var next = config
        next.server = hub
        next.token = client.token
        next.key = key.text
        apply(next)
    }

    /// Deletes everything on the hub and makes a new key; every other device
    /// has to pair again.
    func startOver() async throws {
        guard let client else { throw HubError.notSetUp }
        let fresh = HubClient(base: client.base, token: client.token, key: .new(), device: client.device)
        try await fresh.resetHub()
        try await fresh.claim()
        var next = config
        next.key = fresh.key.text
        apply(next)
    }

    func unpair() {
        var next = config
        next.server = ""
        next.token = ""
        next.key = ""
        stop()
        apply(next)
    }

    func rename(_ device: String) {
        let name = device.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, name != config.device else { return }
        var next = config
        next.device = String(name.prefix(40))
        next.save()
        config = next
    }

    // MARK: code

    /// Language and coloured tokens for text that is code, worked out here:
    /// the hub never sees the text.
    func code(for item: Item) -> CodeHighlighter.Result? {
        guard item.isText, let text = item.text else { return nil }
        if let cached = codeCache[item.id] { return cached }
        let result = CodeHighlighter.shared.analyze(text)
        codeCache[item.id] = result
        return result
    }

    /// Loads the feed and keeps it current until `stop()`.
    func start() {
        stop()
        guard let client else { return }
        liveTask = Task { [weak self] in
            while !Task.isCancelled {
                // A device elsewhere may have started over with a new key.
                do {
                    try await client.verify()
                } catch HubError.otherKey {
                    self?.problem = HubError.otherKey.localizedDescription
                    return
                } catch {}
                await self?.refresh()
                do {
                    let stream = try await client.events()
                    self?.connection = .connected
                    // "hello" only confirms the connection; the feed was just loaded.
                    for try await line in stream.lines where line.hasPrefix("data:") && !line.contains("hello") {
                        await self?.refresh()
                    }
                } catch {
                    // Dropped connections are routine (sleep, Wi-Fi changes); retry below.
                }
                if !Task.isCancelled { self?.connection = .offline }
                try? await Task.sleep(nanoseconds: 3_000_000_000)
            }
        }
    }

    func stop() {
        liveTask?.cancel()
        liveTask = nil
        connection = .connecting
    }

    func refresh() async {
        guard let client else { return }
        do {
            items = try await client.list()
            problem = nil
        } catch is CancellationError {
        } catch let error as URLError where error.code == .cancelled {
        } catch {
            problem = error.localizedDescription
        }
    }

    // MARK: sending

    private func attempt(sending: Bool = false, _ work: (HubClient) async throws -> Void) async {
        guard let client else {
            problem = HubError.notSetUp.localizedDescription
            return
        }
        if sending { self.sending += 1 }
        defer { if sending { self.sending -= 1 } }
        do {
            try await work(client)
            problem = nil
        } catch {
            problem = error.localizedDescription
        }
    }

    func send(text: String) async {
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        await attempt(sending: true) { client in
            try await client.verify() // never send with a key the other devices don't have
            try await client.sendText(text)
        }
        await refresh()
    }

    func send(image data: Data) async {
        await attempt(sending: true) { client in
            try await client.verify()
            // HEIC to JPEG conversion is slow enough to keep off the main thread.
            let prepared = await Task.detached(priority: .userInitiated) { ImagePrep.prepare(data) }.value
            if let prepared {
                try await client.sendData(prepared.data, name: nil, mime: prepared.mime)
            } else {
                try await client.sendData(data, name: nil, mime: "application/octet-stream")
            }
        }
        await refresh()
    }

    /// Handles the system Paste button, which hands over the clipboard
    /// without iOS asking for permission each time.
    nonisolated func paste(_ providers: [NSItemProvider]) {
        for provider in providers {
            if provider.hasItemConformingToTypeIdentifier(UTType.image.identifier) {
                _ = provider.loadDataRepresentation(forTypeIdentifier: UTType.image.identifier) { data, _ in
                    guard let data else { return }
                    Task { await self.send(image: data) }
                }
            } else if provider.canLoadObject(ofClass: URL.self) {
                _ = provider.loadObject(ofClass: URL.self) { url, _ in
                    guard let url else { return }
                    Task { await self.send(text: url.absoluteString) }
                }
            } else if provider.canLoadObject(ofClass: String.self) {
                _ = provider.loadObject(ofClass: String.self) { text, _ in
                    guard let text else { return }
                    Task { await self.send(text: text) }
                }
            }
        }
    }

    // MARK: acting on items

    func copy(_ item: Item) async {
        if let text = item.text, item.isText {
            UIPasteboard.general.string = text
            flash("Copied")
            return
        }
        await attempt { client in
            let data = try await client.raw(item)
            if item.isImage, let image = UIImage(data: data) {
                UIPasteboard.general.image = image
                self.flash("Picture copied")
            } else {
                let type = UTType(mimeType: item.mime ?? "") ?? .data
                UIPasteboard.general.setData(data, forPasteboardType: type.identifier)
                self.flash("Copied")
            }
        }
    }

    func saveToPhotos(_ item: Item) async {
        await attempt { client in
            let data = try await client.raw(item)
            let status = await PHPhotoLibrary.requestAuthorization(for: .addOnly)
            guard status == .authorized || status == .limited else {
                throw HubError.server(0, "Allow Fetch to add photos in Settings > Privacy > Photos.")
            }
            try await PHPhotoLibrary.shared().performChanges {
                PHAssetCreationRequest.forAsset().addResource(with: .photo, data: data, options: nil)
            }
            self.flash("Saved to Photos")
        }
    }

    func share(_ item: Item) async {
        await attempt { client in
            let file = try await client.download(item)
            self.sharing = SharedFile(url: file)
        }
    }

    func togglePin(_ item: Item) async {
        await attempt { try await $0.setPinned(item, !item.pinned) }
        await refresh()
    }

    func delete(_ item: Item) async {
        items.removeAll { $0.id == item.id }
        await attempt { try await $0.delete(item) }
        await refresh()
    }

    func clearUnpinned() async {
        await attempt { try await $0.clearUnpinned() }
        await refresh()
    }

    // MARK: pictures in the feed

    /// Full photos are several megabytes each; rows only ever hold a
    /// screen-sized, decoded copy.
    func thumbnail(for item: Item) async -> UIImage? {
        if let cached = thumbnails.object(forKey: item.id as NSString) { return cached }
        guard let client, let data = try? await client.raw(item) else { return nil }
        let image = await Task.detached(priority: .utility) { FeedModel.downsample(data, maxPixel: 1400) }.value
        if let image { thumbnails.setObject(image, forKey: item.id as NSString) }
        return image
    }

    nonisolated private static func downsample(_ data: Data, maxPixel: Int) -> UIImage? {
        let sourceOptions: [CFString: Any] = [kCGImageSourceShouldCache: false]
        guard let source = CGImageSourceCreateWithData(data as CFData, sourceOptions as CFDictionary) else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: maxPixel,
        ]
        guard let cgImage = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        return UIImage(cgImage: cgImage)
    }

    private func flash(_ message: String) {
        UINotificationFeedbackGenerator().notificationOccurred(.success)
        toast = message
        toastTask?.cancel()
        toastTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 1_600_000_000)
            if !Task.isCancelled { self?.toast = nil }
        }
    }
}
