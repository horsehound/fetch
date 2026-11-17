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
        .background(Color(uiColor: .secondarySystemGroupedBackground))
        .clipShape(shape)
        .overlay { shape.strokeBorder(item.pinned ? Color.primary : Color(uiColor: .separator), lineWidth: 1) }
        .contextMenu { menu }
    }

    // MARK: content

    @ViewBuilder private var content: some View {
        if item.isText {
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
                        .foregroundStyle(.secondary)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(item.name ?? "File")
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Text(ByteCountFormatter.string(fromByteCount: Int64(item.size), countStyle: .file))
                            .font(.caption.monospaced())
                            .foregroundStyle(.secondary)
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
                    .accessibilityLabel("Pinned")
            }
            Text(caption)
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Spacer(minLength: 8)
            if item.isText {
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
        .foregroundStyle(.secondary)
        .accessibilityLabel(label)
    }

    @ViewBuilder private var menu: some View {
        Button("Copy", systemImage: "doc.on.doc") { Task { await model.copy(item) } }
        if item.isImage {
            Button("Save to Photos", systemImage: "square.and.arrow.down") { Task { await model.saveToPhotos(item) } }
        }
        Button("Share", systemImage: "square.and.arrow.up") { Task { await model.share(item) } }
        Button(item.pinned ? "Unpin" : "Pin", systemImage: item.pinned ? "pin.slash" : "pin") {
            Task { await model.togglePin(item) }
        }
        Button("Delete", systemImage: "trash", role: .destructive) { Task { await model.delete(item) } }
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
                Color(uiColor: .tertiarySystemFill)
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
        .background(Color(uiColor: .tertiarySystemFill))
        .task(id: item.id) {
            let loaded = await model.thumbnail(for: item)
            image = loaded
            failed = loaded == nil
        }
    }
}
