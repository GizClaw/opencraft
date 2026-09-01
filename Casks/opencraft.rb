# Homebrew cask for opencraft (macOS, Apple Silicon).
#
# The tap lives in this repository: after each release, refresh the
# version and sha256 below (scripts/update-cask.sh v0.1.0) and merge the
# change so `brew install --cask opencraft` keeps working.
cask "opencraft" do
  version "0.1.0"
  sha256 "3ed9584e1fa732ddb4c7466a499f3841394e75c53d71cbcd7c88530a2c59fc10"

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
