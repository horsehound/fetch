import Foundation
import Security

/// The app and its share extension keep the hub address and token in a shared
/// app group so the extension works without its own setup.
enum AppGroup {
    static let base = "group.com.horsehound.fetch"

    /// SideStore and AltStore re-sign the app with the group renamed to
    /// "<base>.<TEAMID>" and list the new name under ALTAppGroups, so the
    /// group that actually works has to be found at runtime.
    static let identifier: String? = {
        var candidates: [String] = []
        if let listed = Bundle.main.object(forInfoDictionaryKey: "ALTAppGroups") as? [String] {
            candidates += listed.filter { $0.hasPrefix(base) }
        }
        if let team = teamID() {
            candidates.append("\(base).\(team)")
        }
        candidates.append(base)
        return candidates.first {
            FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: $0) != nil
        }
    }()

    /// Falls back to the process's own defaults when no group is available;
    /// the share extension then asks for the hub details itself.
    static let defaults: UserDefaults = identifier.flatMap { UserDefaults(suiteName: $0) } ?? .standard

    /// The signing team ID is the prefix of the default keychain access group.
    private static func teamID() -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.horsehound.fetch.probe",
            kSecAttrAccount as String: "team",
            kSecReturnAttributes as String: true,
        ]
        var result: CFTypeRef?
        var status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound {
            status = SecItemAdd(query as CFDictionary, &result)
        }
        guard status == errSecSuccess,
              let attributes = result as? [String: Any],
              let group = attributes[kSecAttrAccessGroup as String] as? String,
              let prefix = group.split(separator: ".").first
        else { return nil }
        return String(prefix)
    }
}

struct HubConfig: Equatable {
    var server = ""
    var token = ""
    var device = "iPhone"

    var isComplete: Bool { baseURL != nil && !token.trimmingCharacters(in: .whitespaces).isEmpty }

    /// Accepts full http(s) URLs, and fills in the scheme when it is left out:
    /// http for an address on a home network ("192.168.1.20:8787",
    /// "proxmox.lan"), https for a domain ("fetch.example.com").
    var baseURL: URL? {
        var text = server.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }
        if !text.contains("://") {
            let host = text.split(separator: "/").first.map(String.init) ?? text
            let isLocal = host.contains(":") || !host.contains(".")
                || host.hasSuffix(".local") || host.hasSuffix(".lan")
                || host.allSatisfy { $0.isNumber || $0 == "." }
            text = (isLocal ? "http://" : "https://") + text
        }
        while text.hasSuffix("/") { text.removeLast() }
        guard let url = URL(string: text), url.host != nil else { return nil }
        return url
    }

    static func load() -> HubConfig {
        let d = AppGroup.defaults
        var config = HubConfig()
        config.server = d.string(forKey: "server") ?? ""
        config.token = d.string(forKey: "token") ?? ""
        if let name = d.string(forKey: "device"), !name.isEmpty { config.device = name }
        return config
    }

    func save() {
        let d = AppGroup.defaults
        d.set(server.trimmingCharacters(in: .whitespacesAndNewlines), forKey: "server")
        d.set(token.trimmingCharacters(in: .whitespacesAndNewlines), forKey: "token")
        d.set(device.trimmingCharacters(in: .whitespacesAndNewlines), forKey: "device")
    }
}
