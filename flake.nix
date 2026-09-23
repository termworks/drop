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
        };
      in
      {
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
