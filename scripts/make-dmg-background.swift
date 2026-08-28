// Generates the DMG background (800x400 px) used by create-dmg.
// Finder does not scale background images, so the image must be exactly
// the size of the Finder window (800x400 pt).
//
// Usage: swift scripts/make-dmg-background.swift [output.png]
// Default output: build/dmg-background.png

import CoreGraphics
import CoreText
import Foundation
import ImageIO
import UniformTypeIdentifiers

let width = 800
let height = 400

guard let ctx = CGContext(
    data: nil,
    width: width,
    height: height,
    bitsPerComponent: 8,
    bytesPerRow: 0,
    space: CGColorSpaceCreateDeviceRGB(),
    bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
) else {
    fatalError("cannot create drawing context")
}

func rgb(_ r: CGFloat, _ g: CGFloat, _ b: CGFloat, _ a: CGFloat = 1) -> CGColor {
    CGColor(red: r / 255, green: g / 255, blue: b / 255, alpha: a)
}

func makeFont(_ name: String, _ size: CGFloat) -> CTFont {
    CTFontCreateWithName(name as CFString, size, nil)
}

func textWidth(_ text: String, font: CTFont) -> CGFloat {
    let attrs: [NSAttributedString.Key: Any] = [
        NSAttributedString.Key(kCTFontAttributeName as String): font,
    ]
    let line = CTLineCreateWithAttributedString(NSAttributedString(string: text, attributes: attrs))
    return CGFloat(CTLineGetTypographicBounds(line, nil, nil, nil))
}

func drawText(
    _ text: String,
    font: CTFont,
    color: CGColor,
    baselineY: CGFloat,
    centerX: CGFloat = CGFloat(width) / 2
) {
    let attrs: [NSAttributedString.Key: Any] = [
        NSAttributedString.Key(kCTFontAttributeName as String): font,
        NSAttributedString.Key(kCTForegroundColorAttributeName as String): color,
    ]
    let line = CTLineCreateWithAttributedString(NSAttributedString(string: text, attributes: attrs))
    let w = CGFloat(CTLineGetTypographicBounds(line, nil, nil, nil))
    ctx.textPosition = CGPoint(x: centerX - w / 2, y: baselineY)
    CTLineDraw(line, ctx)
}

// Background gradient, matching build/appicon.svg (#1b2230 -> #0f1218).
let gradientColors = [rgb(27, 34, 48), rgb(15, 18, 24)] as CFArray
let locations: [CGFloat] = [0, 1]
let gradient = CGGradient(
    colorsSpace: CGColorSpaceCreateDeviceRGB(),
    colors: gradientColors,
    locations: locations
)!
ctx.drawLinearGradient(
    gradient,
    start: CGPoint(x: 0, y: CGFloat(height)),
    end: CGPoint(x: 0, y: 0),
    options: []
)

// Wordmark: "Open" in the light color, "Craft" in the accent blue.
let titleFont = makeFont("Menlo-Bold", 36)
let openWidth = textWidth("Open", font: titleFont)
let craftWidth = textWidth("Craft", font: titleFont)
drawText(
    "Open",
    font: titleFont,
    color: rgb(230, 233, 239),
    baselineY: 365,
    centerX: CGFloat(width) / 2 - craftWidth / 2
)
drawText(
    "Craft",
    font: titleFont,
    color: rgb(79, 140, 255),
    baselineY: 365,
    centerX: CGFloat(width) / 2 + openWidth / 2
)

// Install hint, bottom center. The Finder-drawn app icon (left) and the
// Applications drop link (right) occupy the middle band of the window.
drawText(
    "拖到 Applications 文件夹以安装",
    font: makeFont("PingFangSC-Semibold", 19),
    color: rgb(230, 233, 239, 0.92),
    baselineY: 75
)
drawText(
    "Drag to Applications to install",
    font: makeFont("Menlo-Regular", 13),
    color: rgb(155, 163, 178, 0.85),
    baselineY: 46
)

let outputPath = CommandLine.arguments.count > 1
    ? CommandLine.arguments[1]
    : "build/dmg-background.png"
let outputURL = URL(fileURLWithPath: outputPath)
guard let destination = CGImageDestinationCreateWithURL(
    outputURL as CFURL,
    UTType.png.identifier as CFString,
    1,
    nil
), let image = ctx.makeImage() else {
    fatalError("cannot create PNG destination")
}
CGImageDestinationAddImage(destination, image, nil)
guard CGImageDestinationFinalize(destination) else {
    fatalError("cannot write \(outputPath)")
}
print("wrote \(outputPath)")
