class Digicli < Formula
  desc "Local-first agentic coding assistant for the terminal, built on Ollama"
  homepage "https://github.com/10txn/digicli"
  url "https://github.com/10txn/digicli/archive/refs/tags/v0.1.3.tar.gz"
  # Filled in by `make formula TAG=v0.1.0` once the tag is pushed.
  sha256 "262dd86a8144831299cb3705a06e6481c79e145fbef793ab9962de0591970211"
  license "MIT"
  head "https://github.com/10txn/digicli.git", branch: "main"

  depends_on "go" => :build

  def install
    # Matches the Makefile's build: stripped, reproducible, version stamped.
    ldflags = "-s -w -X main.version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/digicli"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/digicli --version")
  end
end
