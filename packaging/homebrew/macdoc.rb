# Template for the Homebrew tap formula (owner/homebrew-macdoc, Formula/macdoc.rb).
# Replace the VERSION and SHA256_* placeholders per release; see docs/RELEASING.md.
class Macdoc < Formula
  desc "Safe, local-first macOS CLI that explains why a Mac is full and slow"
  homepage "https://github.com/sagnik26/macdoc"
  version "VERSION" # e.g. 0.1.0, without the v
  license "MIT" # matches LICENSE (MIT)

  on_macos do
    on_arm do
      url "https://github.com/sagnik26/macdoc/releases/download/vVERSION/macdoc_VERSION_darwin_arm64.tar.gz"
      sha256 "SHA256_ARM64"
    end
    on_intel do
      url "https://github.com/sagnik26/macdoc/releases/download/vVERSION/macdoc_VERSION_darwin_amd64.tar.gz"
      sha256 "SHA256_AMD64"
    end
  end

  def install
    bin.install "macdoc"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/macdoc version")
  end
end
