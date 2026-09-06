# Reference copy only. goreleaser regenerates and publishes the real
# formula to github.com/gs-sinha/homebrew-tap on every release (see the
# `brews:` block in .goreleaser.yaml); this file is not consumed by that
# process and is not itself a working tap. It exists so contributors can
# see the formula's expected shape without running a release, and so
# `brew install --build-from-source ./Formula/sapien.rb` works against a
# specific tagged version for local testing.
#
# VERSION/URL/SHA256 below are placeholders for the most recent tag; update
# them (or just delete this comment block) when testing a specific release.
class Sapien < Formula
  desc "Local-first, agent-native API workspace engine"
  homepage "https://github.com/gs-sinha/sapien"
  license "Apache-2.0"
  version "0.1.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/gs-sinha/sapien/releases/download/v0.1.0/sapien_0.1.0_darwin_arm64.tar.gz"
      sha256 "00000000000000000000000000000000000000000000000000000000000000"
    else
      url "https://github.com/gs-sinha/sapien/releases/download/v0.1.0/sapien_0.1.0_darwin_amd64.tar.gz"
      sha256 "00000000000000000000000000000000000000000000000000000000000000"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/gs-sinha/sapien/releases/download/v0.1.0/sapien_0.1.0_linux_arm64.tar.gz"
      sha256 "00000000000000000000000000000000000000000000000000000000000000"
    else
      url "https://github.com/gs-sinha/sapien/releases/download/v0.1.0/sapien_0.1.0_linux_amd64.tar.gz"
      sha256 "00000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    bin.install "sapien"
  end

  test do
    system "#{bin}/sapien", "version"
  end
end
