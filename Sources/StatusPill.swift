import SwiftUI

/// Whether the hub is reachable and what's on its way there: a pill with a
/// tennis ball. Lime ball when connected; indigo, with the ball spinning,
/// while sending; fuchsia with a grey ball when the hub can't be reached.
struct StatusPill: View {
    let connection: Connection
    let sending: Int

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var spin = false

    private enum Look { case connecting, connected, sending, offline }

    private var look: Look {
        if sending > 0 { return .sending }
        switch connection {
        case .connecting: return .connecting
        case .connected: return .connected
        case .offline: return .offline
        }
    }

    private var label: String {
        switch look {
        case .sending: return sending > 1 ? "Sending \(sending)" : "Sending"
        case .connected: return "Connected"
        case .offline: return "Offline"
        case .connecting: return "Connecting"
        }
    }

    var body: some View {
        HStack(spacing: 6) {
            TennisBall(color: ballColor)
                .frame(width: 15, height: 15)
                .rotationEffect(.degrees(spin ? 360 : 0))
            Text(label)
                .font(.footnote.weight(.semibold))
                .foregroundStyle(textColor)
                .contentTransition(.numericText())
        }
        .padding(.leading, 5)
        .padding(.trailing, 10)
        .padding(.vertical, 4)
        .background(fill, in: Capsule())
        .overlay { Capsule().strokeBorder(stroke, lineWidth: 1) }
        .fixedSize()
        .animation(.easeOut(duration: 0.2), value: look)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(label)
        .onChange(of: look, initial: true) { _, now in
            guard !reduceMotion else { return }
            if now == .sending {
                spin = false
                withAnimation(.linear(duration: 0.8).repeatForever(autoreverses: false)) { spin = true }
            } else {
                withAnimation(.easeOut(duration: 0.2)) { spin = false }
            }
        }
    }

    private var ballColor: Color {
        switch look {
        case .connected, .sending: return Theme.lime
        case .connecting: return Theme.muted
        case .offline: return Color(hex: 0x5A5A55)
        }
    }

    private var textColor: Color {
        switch look {
        case .connected: return .white
        case .sending: return .white
        case .connecting: return Theme.muted
        case .offline: return Theme.fuchsiaSoft
        }
    }

    private var fill: Color {
        switch look {
        case .sending: return Theme.indigo
        case .offline: return Theme.fuchsia.opacity(0.08)
        default: return Theme.card
        }
    }

    private var stroke: Color {
        switch look {
        case .sending: return Theme.indigo
        case .offline: return Theme.fuchsia
        default: return Theme.line
        }
    }
}

/// A ball with the two curved seams of a tennis ball.
private struct TennisBall: View {
    let color: Color

    var body: some View {
        Canvas { context, size in
            let s = min(size.width, size.height) / 16
            context.fill(Path(ellipseIn: CGRect(x: s, y: s, width: 14 * s, height: 14 * s)), with: .color(color))
            var seams = Path()
            seams.move(to: CGPoint(x: 3.2 * s, y: 3.4 * s))
            seams.addCurve(to: CGPoint(x: 3.2 * s, y: 12.6 * s), control1: CGPoint(x: 5.6 * s, y: 5 * s), control2: CGPoint(x: 5.6 * s, y: 11 * s))
            seams.move(to: CGPoint(x: 12.8 * s, y: 3.4 * s))
            seams.addCurve(to: CGPoint(x: 12.8 * s, y: 12.6 * s), control1: CGPoint(x: 10.4 * s, y: 5 * s), control2: CGPoint(x: 10.4 * s, y: 11 * s))
            context.stroke(seams, with: .color(Theme.graphite), style: StrokeStyle(lineWidth: 1.6 * s, lineCap: .round))
        }
    }
}
