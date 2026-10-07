# Homebrew cask for opencraft (macOS, arm64 + x86_64).
#
# The tap lives in this repository: after each release, refresh the
# version and sha256 below (scripts/update-cask.sh v0.1.0) and merge the
# change so `brew install --cask opencraft` keeps working.
cask "opencraft" do
  version "0.6.1"
  sha256 "79ffd1b2a130a1684767853645f9f92cf2b5182d2593938f105b8eeb8b513e79"

  url "https://github.com/GizClaw/opencraft/releases/download/v#{version}/" \
      "opencraft-#{version}-macos-universal.dmg"
  name "OpenCraft"
  desc "Local-first work partner built on flowcraft"
  homepage "https://github.com/GizClaw/opencraft"

  depends_on macos: :monterey

  app "OpenCraft.app"

  zap trash: [
    "~/.opencraft",
    "~/Library/Application Support/opencraft",
    "~/Library/Preferences/com.GizClaw.opencraft.plist",
  ]
end
