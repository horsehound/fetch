import Foundation
import ImageIO
import UniformTypeIdentifiers

/// iPhone photos are HEIC, which browsers on Linux and Windows cannot show.
/// Anything that is not already a web-friendly format is converted to JPEG
/// before it leaves the phone.
enum ImagePrep {
    struct Prepared: Sendable {
        let data: Data
        let mime: String
        let ext: String
    }

    static func prepare(_ data: Data) -> Prepared? {
        if let known = sniff(data) {
            return Prepared(data: data, mime: known.mime, ext: known.ext)
        }
        guard let source = CGImageSourceCreateWithData(data as CFData, nil),
              CGImageSourceGetCount(source) > 0
        else { return nil }
        let output = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(output, UTType.jpeg.identifier as CFString, 1, nil)
        else { return nil }
        let options: [CFString: Any] = [kCGImageDestinationLossyCompressionQuality: 0.9]
        // Copying from the source keeps orientation and other metadata intact.
        CGImageDestinationAddImageFromSource(destination, source, 0, options as CFDictionary)
        guard CGImageDestinationFinalize(destination) else { return nil }
        return Prepared(data: output as Data, mime: "image/jpeg", ext: "jpg")
    }

    private static func sniff(_ data: Data) -> (mime: String, ext: String)? {
        let head = [UInt8](data.prefix(12))
        guard head.count >= 12 else { return nil }
        if head.starts(with: [0xFF, 0xD8, 0xFF]) { return ("image/jpeg", "jpg") }
        if head.starts(with: [0x89, 0x50, 0x4E, 0x47]) { return ("image/png", "png") }
        if head.starts(with: [0x47, 0x49, 0x46, 0x38]) { return ("image/gif", "gif") }
        if head.starts(with: [0x52, 0x49, 0x46, 0x46]), Array(head[8..<12]) == [0x57, 0x45, 0x42, 0x50] {
            return ("image/webp", "webp")
        }
        return nil
    }
}
