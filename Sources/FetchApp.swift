import SwiftUI

@main
struct FetchApp: App {
    @StateObject private var model = FeedModel()

    var body: some Scene {
        WindowGroup {
            FeedView()
                .environmentObject(model)
                .tint(Theme.lime)
                .preferredColorScheme(.dark)
                // fetchpair:// links, from the Camera app scanning a QR code or a tapped link.
                .onOpenURL { model.received($0) }
        }
    }
}
