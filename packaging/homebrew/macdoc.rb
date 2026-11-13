# Template for the Homebrew tap formula (owner/homebrew-macdoc, Formula/macdoc.rb).
# Filled in for v0.1.0 (values from the release's checksums.txt). For the next release, update version and both sha256 values; see docs/RELEASING.md.
class Macdoc < Formula
  desc "Safe, local-first macOS CLI that explains why a Mac is full and slow"
  homepage "https://github.com/sagnik26/macdoc"
  version "0.1.0" # e.g. 0.1.0, without the v
  license "MIT" # matches LICENSE (MIT)

  on_macos do
    on_arm do
      url "https://github.com/sagnik26/macdoc/releases/download/v0.1.0/macdoc_0.1.0_darwin_arm64.tar.gz"
      sha256 "9922d3e1fe8ea11677ab2315f66a4147f9619ded5df4cfbd69e24a819e0fffbd"
    end
    on_intel do
      url "https://github.com/sagnik26/macdoc/releases/download/v0.1.0/macdoc_0.1.0_darwin_amd64.tar.gz"
      sha256 "ba57a1950e53c17323e1fe581df8be30e511b7f67dce5024674fa7c13f0c25e0"
    end
  end

  def install
    bin.install "macdoc"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/macdoc version")
  end
end
