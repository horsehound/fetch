import UIKit
import UniformTypeIdentifiers

/// "Fetch" in the share sheet: sends whatever was shared straight to the hub
/// and closes itself.
final class ShareViewController: UIViewController {
    private let spinner = UIActivityIndicatorView(style: .medium)
    private let label = UILabel()
    private let closeButton = UIButton(type: .system)
    private var started = false

    private enum ShareError: LocalizedError {
        case unreadable
        var errorDescription: String? { "Couldn't read what was shared." }
    }

    // MARK: lifecycle

    override func viewDidLoad() {
        super.viewDidLoad()
        // Same palette as the app: graphite card, lime accents.
        let graphite = UIColor(red: 0x24 / 255, green: 0x24 / 255, blue: 0x24 / 255, alpha: 1)
        let lime = UIColor(red: 0xCF / 255, green: 0xFF / 255, blue: 0x04 / 255, alpha: 1)
        overrideUserInterfaceStyle = .dark
        view.backgroundColor = UIColor.black.withAlphaComponent(0.4)

        label.text = "Sending"
        label.font = .preferredFont(forTextStyle: .headline)
        label.textAlignment = .center
        label.numberOfLines = 0

        closeButton.setTitle("Close", for: .normal)
        closeButton.tintColor = lime
        closeButton.isHidden = true
        closeButton.addTarget(self, action: #selector(close), for: .touchUpInside)

        spinner.color = lime
        spinner.startAnimating()

        let stack = UIStackView(arrangedSubviews: [spinner, label, closeButton])
        stack.axis = .vertical
        stack.spacing = 12
        stack.alignment = .center
        stack.isLayoutMarginsRelativeArrangement = true
        stack.layoutMargins = UIEdgeInsets(top: 24, left: 24, bottom: 20, right: 24)
        stack.backgroundColor = graphite
        stack.layer.cornerRadius = 18
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: view.centerYAnchor),
            stack.widthAnchor.constraint(greaterThanOrEqualToConstant: 180),
            stack.widthAnchor.constraint(lessThanOrEqualTo: view.widthAnchor, constant: -64),
        ])
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        guard !started else { return }
        started = true
        begin()
    }

    // MARK: flow

    private func begin() {
        guard let client = HubClient(HubConfig.load()) else {
            askForHub()
            return
        }
        Task {
            do {
                let count = try await sendAll(with: client)
                finish(count == 0 ? "Nothing to send" : count == 1 ? "Sent" : "Sent \(count) items")
            } catch {
                fail(error.localizedDescription)
            }
        }
    }

    private func sendAll(with client: HubClient) async throws -> Int {
        // Never encrypt with a key the other devices no longer use.
        try await client.verify()
        let shared = (extensionContext?.inputItems as? [NSExtensionItem]) ?? []
        let providers = shared.flatMap { $0.attachments ?? [] }
        var sent = 0
        for (index, provider) in providers.enumerated() {
            label.text = providers.count > 1 ? "Sending \(index + 1) of \(providers.count)" : "Sending"
            if try await send(provider, with: client) { sent += 1 }
        }
        return sent
    }

    private func send(_ provider: NSItemProvider, with client: HubClient) async throws -> Bool {
        let types = provider.registeredTypeIdentifiers
        let isFile = provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier)

        if provider.hasItemConformingToTypeIdentifier(UTType.image.identifier) {
            // Photos offers a JPEG next to the HEIC original; take the format
            // other devices can open when there is one.
            let friendly = [UTType.jpeg, .png, .gif, .webP].map(\.identifier).first { types.contains($0) }
            let data = try await loadData(provider, type: friendly ?? UTType.image.identifier)
            let prepared = await Task.detached(priority: .userInitiated) { ImagePrep.prepare(data) }.value
            if let prepared {
                try await client.sendData(prepared.data, name: nil, mime: prepared.mime)
            } else {
                try await client.sendData(data, name: provider.suggestedName, mime: "application/octet-stream")
            }
            return true
        }

        if !isFile, provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
            let url = try await loadURL(provider)
            try await client.sendText(url.absoluteString)
            return true
        }

        if !isFile, provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
            let text = try await loadString(provider)
            try await client.sendText(text)
            return true
        }

        // Everything else is a file: videos, PDFs, archives, documents.
        let copy: URL
        if let type = types.first(where: { Self.isFileContent($0) }) {
            copy = try await loadFile(provider, type: type)
        } else if isFile {
            let original = try await loadURL(provider)
            copy = try Self.temporaryCopy(of: original)
        } else {
            return false
        }
        defer { try? FileManager.default.removeItem(at: copy.deletingLastPathComponent()) }
        let mime = UTType(filenameExtension: copy.pathExtension)?.preferredMIMEType ?? "application/octet-stream"
        try await client.sendFile(copy, name: copy.lastPathComponent, mime: mime)
        return true
    }

    private func finish(_ message: String) {
        spinner.stopAnimating()
        label.text = message
        UINotificationFeedbackGenerator().notificationOccurred(.success)
        Task {
            try? await Task.sleep(nanoseconds: 600_000_000) // long enough to read the result
            extensionContext?.completeRequest(returningItems: nil, completionHandler: nil)
        }
    }

    private func fail(_ message: String) {
        spinner.stopAnimating()
        label.text = message
        closeButton.isHidden = false
    }

    @objc private func close() {
        extensionContext?.cancelRequest(withError: CocoaError(.userCancelled))
    }

    /// Normally the pairing comes from the Fetch app. If this install can't
    /// share settings between the app and the share sheet, ask once here.
    private func askForHub() {
        let alert = UIAlertController(
            title: "Pair the share sheet",
            message: "Paste the pairing link from Add a device on a device that's set up.",
            preferredStyle: .alert
        )
        alert.addTextField { field in
            field.placeholder = "fetchpair://…"
            field.keyboardType = .URL
            field.autocapitalizationType = .none
            field.autocorrectionType = .no
        }
        alert.addAction(UIAlertAction(title: "Cancel", style: .cancel) { [weak self] _ in
            self?.close()
        })
        alert.addAction(UIAlertAction(title: "Send", style: .default) { [weak self, weak alert] _ in
            guard let pairing = try? Seal.Pairing(link: alert?.textFields?.first?.text ?? "") else {
                self?.fail("That isn't a complete pairing link.")
                return
            }
            var config = HubConfig.load()
            config.server = pairing.hub
            config.token = pairing.token
            config.key = pairing.key.text
            config.save()
            self?.begin()
        })
        present(alert, animated: true)
    }

    // MARK: reading what was shared

    /// True for types that carry file contents, as opposed to a link to them.
    private static func isFileContent(_ identifier: String) -> Bool {
        guard let type = UTType(identifier) else { return false }
        return type.conforms(to: .data) && !type.conforms(to: .url)
    }

    private static func temporaryCopy(of url: URL) throws -> URL {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let copy = folder.appendingPathComponent(url.lastPathComponent)
        try FileManager.default.copyItem(at: url, to: copy)
        return copy
    }

    private func loadData(_ provider: NSItemProvider, type: String) async throws -> Data {
        try await withCheckedThrowingContinuation { continuation in
            _ = provider.loadDataRepresentation(forTypeIdentifier: type) { data, error in
                if let data {
                    continuation.resume(returning: data)
                } else {
                    continuation.resume(throwing: error ?? ShareError.unreadable)
                }
            }
        }
    }

    private func loadFile(_ provider: NSItemProvider, type: String) async throws -> URL {
        try await withCheckedThrowingContinuation { continuation in
            _ = provider.loadFileRepresentation(forTypeIdentifier: type) { url, error in
                guard let url else {
                    continuation.resume(throwing: error ?? ShareError.unreadable)
                    return
                }
                // The system removes its file when this block returns, so keep a copy.
                do {
                    continuation.resume(returning: try Self.temporaryCopy(of: url))
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
    }

    private func loadURL(_ provider: NSItemProvider) async throws -> URL {
        try await withCheckedThrowingContinuation { continuation in
            _ = provider.loadObject(ofClass: URL.self) { url, error in
                if let url {
                    continuation.resume(returning: url)
                } else {
                    continuation.resume(throwing: error ?? ShareError.unreadable)
                }
            }
        }
    }

    private func loadString(_ provider: NSItemProvider) async throws -> String {
        try await withCheckedThrowingContinuation { continuation in
            _ = provider.loadObject(ofClass: String.self) { text, error in
                if let text {
                    continuation.resume(returning: text)
                } else {
                    continuation.resume(throwing: error ?? ShareError.unreadable)
                }
            }
        }
    }
}
