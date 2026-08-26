{
  description = "Dev environment for the skills repo (Rust toolchain for the credentials skill).";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }: {
    devShells.aarch64-darwin.default = with nixpkgs.legacyPackages.aarch64-darwin; mkShellNoCC {
      packages = [ cargo rustc ];
    };
  };
}
