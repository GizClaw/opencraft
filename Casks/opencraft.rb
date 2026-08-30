# Homebrew cask for opencraft (macOS, Apple Silicon).
#
# The tap lives in this repository: after each release, refresh the
# version and sha256 below (scripts/update-cask.sh v0.1.0) and merge the
# change so `brew install --cask opencraft` keeps working.
cask "opencraft" do
  version "0.1.0-rc.2"
  sha256 "18106af9bb6c942b58064ecbc5d5bee781f65abe42585fd547f2cf8e4030c002"

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
