import SwiftUI

struct SettingsView: View {
    @EnvironmentObject private var model: FeedModel
    @Environment(\.dismiss) private var dismiss

    @State private var draft = HubConfig()
    @State private var checking = false
    @State private var failure: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("192.168.1.20:8787 or fetch.example.com", text: $draft.server)
                        .keyboardType(.URL)
                        .textContentType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Token", text: $draft.token)
                        .font(.body.monospaced())
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                } header: {
                    Text("Hub")
                } footer: {
                    Text("The hub prints its address and token when it starts. A hub on your home network is reachable on the same Wi-Fi; one on a server with its own domain works from anywhere.")
                }

                Section {
                    TextField("iPhone", text: $draft.device)
                } header: {
                    Text("This device")
                } footer: {
                    Text("Shown next to everything you send from here.")
                }

                if let failure {
                    Section {
                        Label(failure, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(Theme.fuchsia)
                        Button("Save anyway") {
                            model.apply(draft)
                            dismiss()
                        }
                    }
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if checking {
                        ProgressView()
                    } else {
                        Button("Save") { Task { await save() } }
                            .disabled(!draft.isComplete)
                    }
                }
            }
            .onAppear { draft = model.config }
        }
    }

    /// Checks the hub answers with this token before keeping the settings.
    private func save() async {
        guard let client = HubClient(draft) else { return }
        checking = true
        failure = nil
        defer { checking = false }
        do {
            _ = try await client.list()
            model.apply(draft)
            dismiss()
        } catch {
            failure = error.localizedDescription
        }
    }
}
