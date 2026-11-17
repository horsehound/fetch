import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

struct FeedView: View {
    @EnvironmentObject private var model: FeedModel
    @Environment(\.scenePhase) private var scenePhase

    @State private var showSettings = false
    @State private var confirmClear = false
    @State private var draft = ""
    @State private var picked: [PhotosPickerItem] = []
    @FocusState private var typing: Bool

    var body: some View {
        NavigationStack {
            feed
                .navigationTitle("Fetch")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar { toolbar }
                .safeAreaInset(edge: .bottom, spacing: 0) { composer }
                .overlay(alignment: .top) { toastView }
                .background(Theme.graphite)
                .toolbarBackground(Theme.graphite, for: .navigationBar)
        }
        .sheet(isPresented: $showSettings) {
            SettingsView().environmentObject(model)
        }
        .sheet(item: $model.sharing) { file in
            ShareSheet(url: file.url)
        }
        .confirmationDialog("Delete everything that isn't pinned, on every device?", isPresented: $confirmClear, titleVisibility: .visible) {
            Button("Delete unpinned items", role: .destructive) {
                Task { await model.clearUnpinned() }
            }
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { model.start() } else if phase == .background { model.stop() }
        }
        .onChange(of: picked) { _, items in
            guard !items.isEmpty else { return }
            picked = []
            Task { await sendPicked(items) }
        }
        .onAppear {
            if model.client == nil { showSettings = true } else { model.start() }
        }
    }

    // MARK: feed

    @ViewBuilder private var feed: some View {
        if model.client == nil {
            ContentUnavailableView {
                Label("Connect to your hub", systemImage: "antenna.radiowaves.left.and.right")
            } description: {
                Text("Enter the address and token your Fetch hub printed when it started.")
            } actions: {
                Button("Open Settings") { showSettings = true }
                    .buttonStyle(.bordered)
            }
        } else {
            List {
                if let problem = model.problem {
                    Label(problem, systemImage: "exclamationmark.triangle")
                        .font(.footnote)
                        .foregroundStyle(Theme.fuchsia)
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.graphite)
                }
                ForEach(model.ordered) { item in
                    ItemRow(item: item)
                        .listRowInsets(EdgeInsets(top: 6, leading: 16, bottom: 6, trailing: 16))
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.graphite)
                        .swipeActions(edge: .trailing) {
                            Button(role: .destructive) {
                                Task { await model.delete(item) }
                            } label: {
                                Label("Delete", systemImage: "trash")
                            }
                        }
                        .swipeActions(edge: .leading) {
                            Button {
                                Task { await model.togglePin(item) }
                            } label: {
                                Label(item.pinned ? "Unpin" : "Pin", systemImage: item.pinned ? "pin.slash" : "pin")
                            }
                            .tint(Color.gray)
                        }
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .scrollDismissesKeyboard(.immediately)
            .refreshable { await model.refresh() }
            .overlay {
                if model.items.isEmpty && model.problem == nil {
                    ContentUnavailableView(
                        "Nothing here yet",
                        systemImage: "tray",
                        description: Text("Send something from any device and it shows up on all of them.")
                    )
                }
            }
        }
    }

    // MARK: bars

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .topBarLeading) {
            HStack(spacing: 6) {
                Circle()
                    .fill(model.live ? Theme.lime : Theme.fuchsia)
                    .frame(width: 7, height: 7)
                Text(model.sending > 0 ? "sending" : model.live ? "live" : "offline")
                    .font(.caption.monospaced())
                    .foregroundStyle(Theme.muted)
            }
            .accessibilityElement(children: .combine)
        }
        ToolbarItem(placement: .topBarTrailing) {
            Menu {
                Button("Settings", systemImage: "gearshape") { showSettings = true }
                Button("Clear unpinned", systemImage: "trash", role: .destructive) { confirmClear = true }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .accessibilityLabel("More")
        }
    }

    private var composer: some View {
        VStack(spacing: 10) {
            HStack(spacing: 10) {
                // The system Paste button reads the clipboard without a permission prompt.
                PasteButton(supportedContentTypes: [.image, .url, .text]) { providers in
                    model.paste(providers)
                }
                .labelStyle(.titleAndIcon)
                .buttonBorderShape(.capsule)
                // Indigo keeps the white label readable, which the system requires of this button.
                .tint(Theme.indigo)

                PhotosPicker(selection: $picked, maxSelectionCount: 20, matching: .images) {
                    Label("Photos", systemImage: "photo.on.rectangle")
                        .font(.subheadline.weight(.medium))
                        .padding(.horizontal, 14)
                        .frame(height: 34)
                        .background(Theme.card, in: Capsule())
                        .overlay { Capsule().strokeBorder(Theme.line, lineWidth: 1) }
                }
                Spacer(minLength: 0)
            }
            HStack(alignment: .bottom, spacing: 8) {
                TextField("Send text", text: $draft, axis: .vertical)
                    .lineLimit(1...5)
                    .focused($typing)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 9)
                    .background(Theme.card, in: RoundedRectangle(cornerRadius: 18))
                    .overlay { RoundedRectangle(cornerRadius: 18).strokeBorder(Theme.line, lineWidth: 1) }
                Button {
                    let text = draft
                    draft = ""
                    typing = false
                    Task { await model.send(text: text) }
                } label: {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.system(size: 34))
                }
                .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityLabel("Send")
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 10)
        .padding(.bottom, 8)
        .background(Theme.graphite)
        .overlay(alignment: .top) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

    @ViewBuilder private var toastView: some View {
        if let toast = model.toast {
            Text(toast)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.white)
                .padding(.horizontal, 16)
                .padding(.vertical, 9)
                .background(Theme.indigo, in: Capsule())
                .padding(.top, 8)
                .transition(.opacity)
        }
    }

    private func sendPicked(_ items: [PhotosPickerItem]) async {
        for item in items {
            if let data = try? await item.loadTransferable(type: Data.self) {
                await model.send(image: data)
            }
        }
    }
}

/// The system share sheet, for saving to Files or handing an item to another app.
struct ShareSheet: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: [url], applicationActivities: nil)
    }

    func updateUIViewController(_ controller: UIActivityViewController, context: Context) {}
}
