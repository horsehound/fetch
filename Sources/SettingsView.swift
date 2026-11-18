import CoreImage
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject private var model: FeedModel
    @Environment(\.dismiss) private var dismiss

    @State private var device = ""
    @State private var link = ""
    @State private var server = ""
    @State private var token = ""
    @State private var busy = false
    @State private var failure: String?
    @State private var canStartOver = false
    @State private var showAddDevice = false
    @State private var confirmUnpair = false
    @State private var confirmStartOver = false

    var body: some View {
        NavigationStack {
            Form {
                if let pairing = model.config.pairing {
                    paired(pairing)
                } else {
                    unpaired
                }

                Section {
                    TextField("iPhone", text: $device)
                } header: {
                    Text("This device")
                } footer: {
                    Text("Shown next to everything you send from here.")
                }

                if let failure {
                    Section {
                        Label(failure, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(Theme.fuchsia)
                    }
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    if busy {
                        ProgressView()
                    } else {
                        Button("Done") {
                            model.rename(device)
                            dismiss()
                        }
                    }
                }
            }
            .onAppear { device = model.config.device }
            .sheet(isPresented: $showAddDevice) {
                AddDeviceView().environmentObject(model)
            }
            .confirmationDialog("Unpair this iPhone? It forgets the key. The feed stays on your other devices; you'd need a pairing link to come back.",
                                isPresented: $confirmUnpair, titleVisibility: .visible) {
                Button("Unpair", role: .destructive) { model.unpair() }
            }
            .confirmationDialog("Delete everything on the hub, pinned items too, and start over with a new key? Every other device has to pair again.",
                                isPresented: $confirmStartOver, titleVisibility: .visible) {
                Button("Delete everything and start over", role: .destructive) { run { try await model.setUp(server: server, token: token, startOver: true); showAddDevice = true } }
            }
        }
    }

    // MARK: paired

    @ViewBuilder private func paired(_ pairing: Seal.Pairing) -> some View {
        Section {
            LabeledContent("Hub", value: pairing.hub)
            LabeledContent("Key") {
                Text(pairing.key.fingerprint)
                    .font(.body.monospaced())
                    .foregroundStyle(Theme.lime)
            }
            Button("Add a device", systemImage: "qrcode") { showAddDevice = true }
            Button("Unpair this iPhone", role: .destructive) { confirmUnpair = true }
        } header: {
            Text("Hub")
        } footer: {
            Text("End-to-end encrypted: everything is encrypted on your devices, and the hub only stores scrambled data. Every paired device shows the same key.")
        }
    }

    // MARK: not paired yet

    @ViewBuilder private var unpaired: some View {
        Section {
            TextField("fetchpair://…", text: $link, axis: .vertical)
                .font(.footnote.monospaced())
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
            Button("Pair") {
                run {
                    if let invite = Seal.Invite(link: link) {
                        try await model.join(invite)
                        link = ""
                        showAddDevice = true
                        return
                    }
                    let pairing = try Seal.Pairing(link: link)
                    if let problem = await model.pair(pairing) { throw HubError.server(0, problem) }
                    link = ""
                }
            }
            .disabled(link.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || busy)
        } header: {
            Text("Pair with a link")
        } footer: {
            Text("On a device that's set up, open Add a device. Scan its QR code with the Camera app, or copy the pairing link and paste it here. An invite link from someone who runs a hub works too.")
        }

        Section {
            TextField("192.168.1.20:8787 or fetch.example.com", text: $server)
                .keyboardType(.URL)
                .textContentType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
            TextField("Token or invite code", text: $token)
                .font(.body.monospaced())
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
            Button("Set up") {
                run {
                    try await model.setUp(server: server, token: token, startOver: false)
                    showAddDevice = true
                }
            }
            .disabled(server.isEmpty || busy)
            if canStartOver {
                Button("Start over with a new key…", role: .destructive) { confirmStartOver = true }
            }
        } header: {
            Text("New here, or your own hub?")
        } footer: {
            Text("For your own hub, enter the token it printed when it started. With an invite code, or on a public hub (leave the code empty), you get a space of your own. Either way this iPhone creates the encryption key, and you add your other devices from here.")
        }
    }

    private func run(_ work: @escaping () async throws -> Void) {
        busy = true
        failure = nil
        Task {
            defer { busy = false }
            do {
                try await work()
                canStartOver = false
            } catch {
                failure = error.localizedDescription
                if case HubError.alreadySetUp = error { canStartOver = true }
            }
        }
    }
}

/// "Add a device": the pairing link as a QR code, to scan with another phone's
/// camera, and as text to copy into a computer.
struct AddDeviceView: View {
    @EnvironmentObject private var model: FeedModel
    @Environment(\.dismiss) private var dismiss
    @State private var confirmStartOver = false
    @State private var failure: String?

    var body: some View {
        NavigationStack {
            ScrollView {
                if let pairing = model.config.pairing {
                    VStack(spacing: 18) {
                        Text("Scan this with the other phone's camera. On a computer, paste the link into the Fetch app, a browser, or fetch pair.")
                            .font(.subheadline)
                            .foregroundStyle(Theme.muted)
                            .multilineTextAlignment(.center)
                        if let qr = QRCode.image(for: pairing.link) {
                            Image(uiImage: qr)
                                .interpolation(.none)
                                .resizable()
                                .scaledToFit()
                                .frame(maxWidth: 280)
                                .padding(14)
                                .background(Color.white, in: RoundedRectangle(cornerRadius: 12))
                                .accessibilityLabel("Pairing QR code")
                        }
                        HStack(spacing: 10) {
                            Button {
                                UIPasteboard.general.string = pairing.link
                                model.toast = "Link copied"
                            } label: {
                                Label("Copy link", systemImage: "doc.on.doc")
                            }
                            .buttonStyle(.borderedProminent)
                            .tint(Theme.lime)
                            .foregroundStyle(Theme.graphite)
                            ShareLink(item: pairing.link) {
                                Label("Share", systemImage: "square.and.arrow.up")
                            }
                            .buttonStyle(.bordered)
                        }
                        Text("Whoever has this link can read and change everything you send. Only use it for your own devices.")
                            .font(.footnote)
                            .foregroundStyle(Theme.fuchsiaSoft)
                            .multilineTextAlignment(.center)
                        Text("Key \(pairing.key.fingerprint), the same on every paired device.")
                            .font(.footnote.monospaced())
                            .foregroundStyle(Theme.muted)
                        if let failure {
                            Text(failure).font(.footnote).foregroundStyle(Theme.fuchsia)
                        }
                        Button("Start over with a new key…", role: .destructive) { confirmStartOver = true }
                            .font(.footnote)
                            .padding(.top, 8)
                    }
                    .padding(20)
                }
            }
            .background(Theme.graphite)
            .navigationTitle("Add a device")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
            .confirmationDialog("Start over with a new key? This deletes everything on the hub, pinned items too, and every other device has to pair again.",
                                isPresented: $confirmStartOver, titleVisibility: .visible) {
                Button("Delete everything and start over", role: .destructive) {
                    Task {
                        do { try await model.startOver() } catch { failure = error.localizedDescription }
                    }
                }
            }
        }
    }
}

enum QRCode {
    static func image(for text: String) -> UIImage? {
        let filter = CIFilter(name: "CIQRCodeGenerator")
        filter?.setValue(Data(text.utf8), forKey: "inputMessage")
        filter?.setValue("M", forKey: "inputCorrectionLevel")
        guard let output = filter?.outputImage?.transformed(by: CGAffineTransform(scaleX: 10, y: 10)),
              let cg = CIContext().createCGImage(output, from: output.extent)
        else { return nil }
        return UIImage(cgImage: cg)
    }
}
