{
  description = "drop distributed file transfer";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };

        # Everything needed to compile, vet and test. CI enters this rather than the full shell so
        # it does not pull the release tooling on every job.
        buildTools = [
          pkgs.go
          pkgs.gopls
          pkgs.gotools
          pkgs.go-tools
          pkgs.golangci-lint
          pkgs.delve
        ];
      in
      {
        devShells.ci = pkgs.mkShell { packages = buildTools; };

        devShells.default = pkgs.mkShell {
          packages = buildTools ++ [
            pkgs.goreleaser
            pkgs.git-cliff
            pkgs.gh
            pkgs.mdbook
            # Only for `make compress`, which is opt-in: it trades startup time for disk.
            pkgs.upx
          ];

          # bin is pure Go; cgo would link the system resolver and pin the binary to this host's
          # libc, which on Nix is an absolute /nix/store path.
          CGO_ENABLED = "0";
        };
      }
    );
}
