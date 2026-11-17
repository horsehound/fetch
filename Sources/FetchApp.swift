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
        }
    }
}
