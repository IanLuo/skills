{
  description = "Dev environment and per-project build outputs for the skills repo";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs, ... }:
    let
      # Every project is exposed for these systems. Building a project's source
      # for a system other than the host needs a builder for it (e.g.
      # nixpkgs.linux-builder or a remote builder); the outputs still evaluate.
      systems = [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ];
      eachSystem = nixpkgs.lib.genAttrs systems;
      perSystem = f: eachSystem (system: f nixpkgs.legacyPackages.${system});
    in {
      # One output per project under src/. Named by project, not by binary, so
      # `.#credentials` addresses both output families the same way. There is
      # deliberately no `default`: "all projects" cannot be one derivation, and a
      # joined default would make a broken project fail the build of a healthy
      # one. Name the project you want.
      #
      # `fs` used to be built here as `./src/flagship`. It is a tool with its own
      # build, tests and release, so it moved to its own repository
      # (~/Documents/apps/fs). Nothing in this repo builds it any more.
      packages = perSystem (pkgs: {
        credentials = pkgs.rustPlatform.buildRustPackage {
          pname = "cred-run";
          version = "0.1.0";
          src = ./src/credentials;
          cargoLock.lockFile = ./src/credentials/Cargo.lock;
          meta.description = "Resolve a cred profile's secrets, inject them into a child process, and scrub them from its output";
        };

      });

      # A shell per project (only that project's toolchain), plus a default that
      # unions them so one `nix develop` at the repo root still gets everything.
      devShells = eachSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          credentials = pkgs.mkShell {
            packages = with pkgs; [ cargo rustc clippy rustfmt ];
          };
        in {
          inherit credentials;
          default = pkgs.mkShell {
            inputsFrom = [ credentials ];
          };
        });
    };
}
