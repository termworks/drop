{
  description = "drop distributed file transfer";

  nixConfig = {
    extra-substituters = [ "https://termworks.cachix.org" ];
    extra-trusted-public-keys = [
      "termworks.cachix.org-1:Ty7sSVALfD5ajbcWBIdaNHcaEx3fEmVrOo+rSzy0mvE="
    ];
  };

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";

    # nixGL puts the host's GPU drivers under a program built here, which is what lets a window
    # open on a machine that is not NixOS. It gets a nixpkgs of its own, pinned: nixpkgs after
    # 2026-04 dropped the `kernel` argument nixGL's NVIDIA wrapper passes, and drop itself should
    # not be held back to wait for that.
    nixpkgs-gl.url = "github:NixOS/nixpkgs?rev=4c1018dae018162ec878d42fec712642d214fdfa";
    nixgl = {
      url = "github:nix-community/nixGL?rev=b6105297e6f0cd041670c3e8628394d4ee247ed5";
      inputs.nixpkgs.follows = "nixpkgs-gl";
      inputs.flake-utils.follows = "flake-utils";
    };
  };

  outputs =
    { nixpkgs, flake-utils, nixgl, nixpkgs-gl, ... }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        lib = pkgs.lib;
        version = builtins.head (builtins.match
          ''.*version[[:space:]]*=[[:space:]]*"([^"]+)".*''
          (builtins.readFile ./src/main.go));
        drop = pkgs.buildGoModule {
          pname = "drop";
          inherit version;
          src = lib.cleanSource ./.;
          vendorHash = "sha256-sV+7iZoxLMqDNbXy48FtYXwVTPYWNRCDwxcRUXRVKSc=";
          subPackages = [ "src" ];
          env.CGO_ENABLED = "0";
          ldflags = [ "-s" "-w" ];
          doCheck = false;
          postInstall = ''
            mv "$out/bin/src" "$out/bin/drop"
          '';
          doInstallCheck = true;
          nativeInstallCheckInputs = [ pkgs.binutils ];
          installCheckPhase = ''
            runHook preInstallCheck
            test "$("$out/bin/drop" --version)" = "drop version ${version}"
            "$out/bin/drop" --help
            if readelf -l "$out/bin/drop" | grep -q 'program interpreter'; then
              echo "error: drop requests a dynamic loader" >&2
              exit 1
            fi
            if readelf -d "$out/bin/drop" | grep -q NEEDED; then
              echo "error: drop has dynamic dependencies" >&2
              exit 1
            fi
            runHook postInstallCheck
          '';
          meta = {
            description = "Peer-to-peer file transfer and communication";
            homepage = "https://github.com/termworks/drop";
            mainProgram = "drop";
            platforms = lib.platforms.linux;
          };
        };
        dropApp = {
          type = "app";
          program = "${drop}/bin/drop";
          meta.description = "Run Drop";
        };

        # The Android SDK is unfree and its licence is accepted by whoever builds, not by the
        # expression, so it needs an instance of nixpkgs that says so.
        android = import nixpkgs {
          inherit system;
          config = {
            allowUnfreePredicate = _: true;
            android_sdk.accept_license = true;
          };
        };

        # Pinned rather than "latest": a moving SDK or NDK changes the APK without a commit.
        sdk =
          (android.androidenv.composeAndroidPackages {
            platformVersions = [ "35" ];
            buildToolsVersions = [ "35.0.0" ];
            ndkVersions = [ "27.2.12479018" ];
            includeNDK = true;

            # An emulator to run it on, x86_64 so KVM can carry it. An ARM image on an x86 host is
            # translated instruction by instruction and is far too slow to test a network daemon.
            includeEmulator = true;
            includeSystemImages = true;
            systemImageTypes = [ "google_apis" ];
            abiVersions = [ "x86_64" ];
          }).androidsdk;

        # The driver this machine actually has, read by .env.lua before the shell is built. Empty in
        # CI and in any pure evaluation, which picks the mesa wrapper and never builds NVIDIA's.
        nvidiaVersion = builtins.getEnv "NVIDIA_VERSION";
        hasNvidia = nvidiaVersion != "";

        gl = import "${nixgl}/default.nix" (
          {
            pkgs = import nixpkgs-gl {
              inherit system;
              config = {
                allowUnfree = true;
                nvidia.acceptLicense = true;
              };
            };
          }
          // nixpkgs.lib.optionalAttrs hasNvidia {
            inherit nvidiaVersion;
            nvidiaHash = null;
          }
        );

        # One name, whatever the hardware: `nixGL scrcpy` rather than a command per vendor.
        nixGL = pkgs.runCommand "nixGL" { } ''
          mkdir -p $out/bin
          ln -s ${
            if hasNvidia then "${gl.nixGLNvidia}/bin/nixGLNvidia-${nvidiaVersion}" else "${gl.nixGLIntel}/bin/nixGLIntel"
          } $out/bin/nixGL
        '';

        # For looking at a device rather than building for one: kept out of the android shell, so CI
        # builds the APK without pulling a graphics stack it never opens.
        guiTools = [
          pkgs.scrcpy
          nixGL
        ];

        # Everything needed to compile, vet and test, without the tools only a release needs.
        buildTools = [
          pkgs.go
          pkgs.gopls
          pkgs.gotools
          pkgs.go-tools
          pkgs.golangci-lint
          pkgs.delve
        ];

        # Everything needed to turn the Go core into an AAR and the Kotlin shell into an APK.
        #
        # gomobile builds against the SDK, so it carries the same unfree licence and comes from the
        # instance that accepts it. The rest are free and come from the ordinary one.
        androidTools = [
          sdk
          android.gomobile
          pkgs.jdk17_headless
          pkgs.gradle
          pkgs.kotlin
        ];

        androidEnv = {
          ANDROID_HOME = "${sdk}/libexec/android-sdk";
          ANDROID_SDK_ROOT = "${sdk}/libexec/android-sdk";
          ANDROID_NDK_ROOT = "${sdk}/libexec/android-sdk/ndk-bundle";
          JAVA_HOME = "${pkgs.jdk17_headless}";
          # Gradle picks the build tools out of the SDK by exact version, and aapt2 in the store is
          # not writable, so gradle is told to use the one it was given.
          GRADLE_OPTS = "-Dorg.gradle.project.android.aapt2FromMavenOverride=${sdk}/libexec/android-sdk/build-tools/35.0.0/aapt2";

          # gomobile keeps its toolchain under the first GOPATH entry, and its wrapper appends its
          # own store path. With GOPATH unset, as on a CI runner, that store path is the only entry
          # and `gomobile init` fails trying to write into it.
          shellHook = ''
            export GOPATH="''${GOPATH:-$HOME/go}"
          '';
        };
      in
      {
        packages = lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          inherit drop;
          default = drop;
        };
        apps = lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          drop = dropApp;
          default = dropApp;
        };
        checks = lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux { inherit drop; };

        # For a job that wants the toolchain without the release tools. The release workflow uses
        # actions/setup-go rather than entering this.
        devShells.ci = pkgs.mkShell { packages = buildTools; };

        # What the APK is built in, here and in CI.
        devShells.android = pkgs.mkShell (
          androidEnv
          // {
            packages = buildTools ++ androidTools;

            # gomobile binds through the NDK, which is cgo.
            CGO_ENABLED = "1";
          }
        );

        devShells.default = pkgs.mkShell (
          androidEnv
          // {
            packages =
              buildTools
              ++ androidTools
              ++ guiTools
              ++ [
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
            # needs cgo. Run that one as `CGO_ENABLED=1 go test -race ./...`. The Android build sets
            # it back where it needs it.
            CGO_ENABLED = "0";
          }
        );
      }
    );
}
