import SwiftUI

/// Graphite Black, Cyber Lime, Electric Indigo, Neon Fuchsia. One dark theme,
/// the same one the hub's web page uses.
enum Theme {
    static let graphite = Color(hex: 0x1C1C1C)
    static let card = Color(hex: 0x242424)
    static let inset = Color(hex: 0x151515)
    static let line = Color(hex: 0x393939)
    static let muted = Color(hex: 0x9A9A94)

    static let lime = Color(hex: 0xCFFF04)
    static let indigo = Color(hex: 0x5D00FF)
    static let fuchsia = Color(hex: 0xFF2E9A)

    // Tints that stay readable as text on graphite.
    static let limeSoft = Color(hex: 0xE6FF7A)
    static let indigoSoft = Color(hex: 0xB79CFF)
    static let fuchsiaSoft = Color(hex: 0xFF8CC8)

    /// Colour for one highlight class sent by the hub (see hub/highlight.go).
    static func code(_ kind: String) -> Color {
        switch kind {
        case "kw", "del": return fuchsia
        case "str": return limeSoft
        case "fn", "key", "add": return lime
        case "num", "typ", "var": return indigoSoft
        case "attr": return fuchsiaSoft
        case "com", "meta": return Color(hex: 0x7D7D77)
        default: return Color(hex: 0xF5F5F2)
        }
    }
}

extension Color {
    init(hex: UInt32) {
        self.init(
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255
        )
    }
}
