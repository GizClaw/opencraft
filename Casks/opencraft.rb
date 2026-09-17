# Homebrew cask for opencraft (macOS, Apple Silicon).
#
# The tap lives in this repository: after each release, refresh the
# version and sha256 below (scripts/update-cask.sh v0.1.0) and merge the
# change so `brew install --cask opencraft` keeps working.
cask "opencraft" do
  version "0.5.3"
  sha256 "ae3e575d0bfc78dd72d7682819b8ae8e85c59b0b2a413776e32711bbcd69576a"

  url "https://github.com/GizClaw/opencraft/releases/download/v#{version}/" \
      "opencraft-#{version}-macos-universal.dmg"
  name "OpenCraft"
  desc "Local-first work partner built on flowcraft"
  homepage "https://github.com/GizClaw/opencraft"

  depends_on macos: :big_sur

  app "OpenCraft.app"

  zap trash: [
    "~/.opencraft",
    "~/Library/Application Support/opencraft",
    "~/Library/Preferences/com.GizClaw.opencraft.plist",
  ]
end
