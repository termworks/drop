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

        # Everything needed to compile, vet and test, without the tools only a release needs.
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
        # For a job that wants the toolchain without the release tools. The release workflow uses
        # actions/setup-go rather than entering this.
        devShells.ci = pkgs.mkShell { packages = buildTools; };

        devShells.default = pkgs.mkShell {
          packages = buildTools ++ [
            # `make changelog` shells out to this.
            pkgs.git-cliff
            pkgs.gh
            # The release workflow packs with `upx -9`; this is here to reproduce that locally.
            # It takes the binary from 19 MB to 6.8 MB and costs 0.054s of startup against
            # 0.003s, because a packed binary unpacks itself every time.
            pkgs.upx
          ];

          # drop is pure Go; cgo would link the system resolver and pin the binary to this host's
          # libc, which on Nix is an absolute /nix/store path.
          #
          # It also means `go test -race` will not build in this shell, because the race detector
          # needs cgo. Run that one as `CGO_ENABLED=1 go test -race ./...`.
          CGO_ENABLED = "0";
        };
      }
    );
}
