import SwiftUI

struct ItemRow: View {
    @EnvironmentObject private var model: FeedModel
    let item: Item
    @State private var expanded = false

    private var shape: RoundedRectangle { RoundedRectangle(cornerRadius: 12) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            content
            footer
        }
        .background(Theme.card)
        .clipShape(shape)
        .overlay { shape.strokeBorder(item.pinned ? Theme.lime : Theme.line, lineWidth: 1) }
        .contextMenu { menu }
    }

    // MARK: content

    @ViewBuilder private var content: some View {
        if item.broken {
            Label("Can't decrypt this item: it was sent with a different key.", systemImage: "lock.slash")
                .font(.subheadline)
                .foregroundStyle(Theme.muted)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 14)
                .padding(.top, 12)
        } else if item.isText, let code {
            Button {
                Task { await model.copy(item) }
            } label: {
                codeBlock(code)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("\(analysis?.label ?? "Code"): \(item.text ?? "")")
            .accessibilityHint("Copies this code")
        } else if item.isText {
            Button {
                Task { await model.copy(item) }
            } label: {
                Text(item.text ?? "")
                    .lineLimit(expanded ? nil : 8)
                    .multilineTextAlignment(.leading)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 14)
                    .padding(.top, 12)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityHint("Copies this text")
        } else if item.isImage {
            Button {
                Task { await model.copy(item) }
            } label: {
                HubImage(item: item)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(item.name ?? "Picture")
            .accessibilityHint("Copies this picture")
        } else {
            Button {
                Task { await model.share(item) }
            } label: {
                HStack(spacing: 12) {
                    Image(systemName: "doc")
                        .font(.title2)
                        .foregroundStyle(Theme.indigoSoft)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(item.name ?? "File")
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Text(ByteCountFormatter.string(fromByteCount: Int64(item.size), countStyle: .file))
                            .font(.caption.monospaced())
                            .foregroundStyle(Theme.muted)
                    }
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 14)
                .padding(.top, 12)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityHint("Opens the share sheet")
        }
    }

    private var footer: some View {
        HStack(spacing: 4) {
            if item.pinned {
                Image(systemName: "pin.fill")
                    .font(.caption2)
                    .foregroundStyle(Theme.lime)
                    .accessibilityLabel("Pinned")
            }
            Text(caption)
                .font(.caption.monospaced())
                .foregroundStyle(Theme.muted)
                .lineLimit(1)
            Spacer(minLength: 8)
            if item.broken {
                icon("trash", "Delete") { Task { await model.delete(item) } }
            } else if item.isText {
                if isLong {
                    icon(expanded ? "chevron.up" : "chevron.down", expanded ? "Show less" : "Show all") {
                        expanded.toggle()
                    }
                }
                if let link {
                    Link(destination: link) {
                        Image(systemName: "safari").frame(width: 36, height: 36)
                    }
                    .accessibilityLabel("Open link")
                }
                icon("doc.on.doc", "Copy") { Task { await model.copy(item) } }
            } else {
                if item.isImage {
                    icon("square.and.arrow.down", "Save to Photos") { Task { await model.saveToPhotos(item) } }
                }
                icon("square.and.arrow.up", "Share") { Task { await model.share(item) } }
            }
        }
        .padding(.leading, 14)
        .padding(.trailing, 6)
        .frame(minHeight: 40)
    }

    private func icon(_ symbol: String, _ label: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.footnote)
                .frame(width: 36, height: 36)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .foregroundStyle(Theme.muted)
        .accessibilityLabel(label)
    }

    @ViewBuilder private var menu: some View {
        if !item.broken {
            actions
        }
        Button("Delete", systemImage: "trash", role: .destructive) { Task { await model.delete(item) } }
    }

    @ViewBuilder private var actions: some View {
        Button("Copy", systemImage: "doc.on.doc") { Task { await model.copy(item) } }
        if item.isImage {
            Button("Save to Photos", systemImage: "square.and.arrow.down") { Task { await model.saveToPhotos(item) } }
        }
        Button("Share", systemImage: "square.and.arrow.up") { Task { await model.share(item) } }
        Button(item.pinned ? "Unpin" : "Pin", systemImage: item.pinned ? "pin.slash" : "pin") {
            Task { await model.togglePin(item) }
        }
    }

    // MARK: code

    /// The item's text with its highlight classes applied as colours,
    /// or nil when it isn't code. Worked out on this device.
    private var analysis: CodeHighlighter.Result? { model.code(for: item) }

    private var code: AttributedString? {
        guard let tokens = analysis?.tokens, !tokens.isEmpty else { return nil }
        var out = AttributedString()
        for token in tokens where token.count == 2 {
            var part = AttributedString(token[1])
            part.swiftUI.foregroundColor = Theme.code(token[0])
            if token[0] == "add" { part.swiftUI.backgroundColor = Theme.lime.opacity(0.1) }
            if token[0] == "del" { part.swiftUI.backgroundColor = Theme.fuchsia.opacity(0.12) }
            out.append(part)
        }
        return out
    }

    /// Darker inset, monospace, language chip: unmistakably not plain text.
    private func codeBlock(_ code: AttributedString) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text((analysis?.label ?? "Code").uppercased())
                .font(.system(size: 10, weight: .semibold, design: .monospaced))
                .foregroundStyle(.white)
                .padding(.horizontal, 7)
                .padding(.vertical, 3)
                .background(Theme.indigo, in: RoundedRectangle(cornerRadius: 5))
            Text(code)
                .font(.system(.footnote, design: .monospaced))
                .lineLimit(expanded ? nil : 8)
                .multilineTextAlignment(.leading)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.inset)
        .contentShape(Rectangle())
    }

    // MARK: helpers

    /// Where the item came from and how long ago.
    private var caption: String {
        let from = item.device ?? "unknown"
        let when = item.date.formatted(.relative(presentation: .numeric, unitsStyle: .abbreviated))
        return from + " · " + when
    }

    private var isLong: Bool {
        let text = item.text ?? ""
        return text.count > 400 || text.filter { $0 == "\n" }.count >= 8
    }

    /// Set when the whole item is a single web link.
    private var link: URL? {
        let text = (item.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        guard text.hasPrefix("http://") || text.hasPrefix("https://"),
              !text.contains(where: \.isWhitespace),
              let url = URL(string: text)
        else { return nil }
        return url
    }
}

/// A picture from the hub, loaded with the access token and shown downsampled.
struct HubImage: View {
    @EnvironmentObject private var model: FeedModel
    let item: Item
    @State private var image: UIImage?
    @State private var failed = false

    var body: some View {
        ZStack {
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: .infinity, maxHeight: 380)
            } else {
                Theme.inset
                    .frame(height: 180)
                if failed {
                    Image(systemName: "photo")
                        .foregroundStyle(.secondary)
                } else {
                    ProgressView()
                }
            }
        }
        .frame(maxWidth: .infinity)
        .background(Theme.inset)
        .task(id: item.id) {
            let loaded = await model.thumbnail(for: item)
            image = loaded
            failed = loaded == nil
        }
    }
}
