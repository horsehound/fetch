import ImageIO
import Photos
import SwiftUI
import UIKit
import UniformTypeIdentifiers

struct SharedFile: Identifiable {
    let id = UUID()
    let url: URL
}

@MainActor
final class FeedModel: ObservableObject {
    @Published private(set) var items: [Item] = []
    @Published private(set) var config = HubConfig.load()
    @Published private(set) var problem: String?
    @Published private(set) var live = false
    @Published private(set) var sending = 0
    @Published var toast: String?
    @Published var sharing: SharedFile?

    private var liveTask: Task<Void, Never>?
    private var toastTask: Task<Void, Never>?
    private let thumbnails = NSCache<NSString, UIImage>()

    var client: HubClient? { HubClient(config) }

    /// Pinned items first, then newest first (the order the hub sends).
    var ordered: [Item] { items.filter(\.pinned) + items.filter { !$0.pinned } }

    // MARK: connection

    func apply(_ new: HubConfig) {
        new.save()
        config = new
        items = []
        problem = nil
        start()
    }

    /// Loads the feed and keeps it current until `stop()`.
    func start() {
        stop()
        guard let client else { return }
        liveTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                do {
                    let stream = try await client.events()
                    for try await line in stream.lines where line.hasPrefix("data:") {
                        self?.live = true
                        // "hello" only confirms the connection; the feed was just loaded.
                        if !line.contains("hello") { await self?.refresh() }
                    }
                } catch {
                    // Dropped connections are routine (sleep, Wi-Fi changes); retry below.
                }
                self?.live = false
                try? await Task.sleep(nanoseconds: 3_000_000_000)
            }
        }
    }

    func stop() {
        liveTask?.cancel()
        liveTask = nil
        live = false
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

    private func attempt(_ work: (HubClient) async throws -> Void) async {
        guard let client else {
            problem = HubError.notSetUp.localizedDescription
            return
        }
        sending += 1
        defer { sending -= 1 }
        do {
            try await work(client)
            problem = nil
        } catch {
            problem = error.localizedDescription
        }
    }

    func send(text: String) async {
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        await attempt { try await $0.sendText(text) }
        await refresh()
    }

    func send(image data: Data) async {
        await attempt { client in
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
