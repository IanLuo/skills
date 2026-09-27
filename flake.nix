{
  description = "Dev environment and per-project build outputs for the skills repo";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs, ... }:
    let
      # What the build is a build OF, stamped into the binary so it can say so.
      #
      # This is the only place the revision can be read: a nix build is
      # hermetic, so the sandbox has no .git to ask, and the value has to come
      # from the flake evaluation. `self` carries one of three things, and all
      # three are reported as they are rather than smoothed over:
      #
      #   self.rev        a clean git checkout — the revision itself
      #   self.dirtyRev   tracked files modified — "<revision>-dirty", which
      #                   says both the revision and that the tree was not
      #                   clean. Untracked files do not dirty a flake to nix,
      #                   and nix copies tracked files only, so they cannot
      #                   reach the build either.
      #   neither         no git at all — a path or a tarball. There is no
      #                   revision, and the build says "unknown" instead of
      #                   naming one it was not built from.
      #
      # A build id that can be wrong is worse than none: three builds of this
      # binary sat on one machine, all different, and nothing in the artifact
      # could say which was which.
      buildRevision =
        if self ? rev then self.rev
        else if self ? dirtyRev then builtins.replaceStrings [ "-dirty" ] [ "" ] self.dirtyRev
        else "unknown";
      buildDirty =
        if self ? dirtyRev then "true"
        else if self ? rev then "false"
        else "unknown";

      # Every project is exposed for these systems. Building a project's source
      # for a system other than the host needs a builder for it (e.g.
      # nixpkgs.linux-builder or a remote builder); the outputs still evaluate.
      systems = [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ];
      eachSystem = nixpkgs.lib.genAttrs systems;
      perSystem = f: eachSystem (system: f nixpkgs.legacyPackages.${system});
    in {
      # One output per project under src/. Named by project, not by binary, so
      # `.#credentials` and `.#flagship` address both output families the same
      # way. There is deliberately no `default`: "all projects" cannot be one
      # derivation, and a joined default would make a broken project fail the
      # build of a healthy one. Name the project you want.
      packages = perSystem (pkgs: {
        credentials = pkgs.rustPlatform.buildRustPackage {
          pname = "cred-run";
          version = "0.1.0";
          src = ./src/credentials;
          cargoLock.lockFile = ./src/credentials/Cargo.lock;
          meta.description = "Resolve a cred profile's secrets, inject them into a child process, and scrub them from its output";
        };

        # go.mod requires go >= 1.26.7 and nixpkgs' default `go` is 1.26.5, so the
        # toolchain has to be named. buildGoModule sets GOTOOLCHAIN=local, which is
        # why this is an error rather than a silent toolchain download: without it
        # `go build` fetches whatever go.mod asks for and the build is no longer
        # reproducible.
        #
        # Dependencies are vendored into src/flagship/vendor, committed, with
        # vendorHash = null, because a build delegated to the nix daemon cannot
        # reach a proxy configured only in the client environment — the module
        # fetch could not run here at all. Vendoring makes the build hermetic and
        # offline. If go.mod changes, regenerate the tree from the module cache:
        #   nix develop .#flagship -c bash -c \
        #     'cd src/flagship && GOPROXY=off GOFLAGS=-mod=mod go mod vendor'
        # and `git add` the result — nix copies only tracked files.
        flagship = (pkgs.buildGoModule.override { go = pkgs.go_1_27; }) {
          pname = "fs";
          version = "0.1.0";
          src = ./src/flagship;
          vendorHash = null;
          # The command, not the library at the module root.
          subPackages = [ "cmd" ];
          # Go names an installed binary after its package directory — here `cmd`;
          # the skill expects `fs`.
          postInstall = "mv $out/bin/cmd $out/bin/fs";
          # The build names itself. These two -X flags set the variables
          # cmd/version.go declares, so `fs version` reports the revision this
          # binary is a build of, whether that tree was dirty, and its own path.
          # Without them the variables keep their "unknown" defaults, which is
          # what a plain `go build` reports.
          ldflags = [
            "-X main.buildRevision=${buildRevision}"
            "-X main.buildDirty=${buildDirty}"
          ];
          # cmd/main_test.go shells out to git, so the check phase needs it on PATH.
          # jq too: the batch playbook's steps read the members' records through
          # it, and tests that run those steps end to end need the same jq the
          # steps expect — a check phase without it exercises the fail-closed
          # guard instead of the step.
          nativeCheckInputs = [ pkgs.git pkgs.jq ];
          meta.description = "Event-sourced project, task, and dispatch store for agent orchestration";
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
          flagship = pkgs.mkShell {
            packages = [ pkgs.go_1_27 pkgs.gopls ];
          };
        in {
          inherit credentials flagship;
          default = pkgs.mkShell {
            inputsFrom = [ credentials flagship ];
          };
        });
    };
}
