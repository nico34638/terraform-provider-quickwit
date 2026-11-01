{
  description = "Terraform provider for Quickwit";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            opentofu
            curl
            jq
            sox
            cargo
            protobuf
          ];
          shellHook = ''
            # terraform-plugin-testing drives acceptance tests through this binary.
            export TF_ACC_TERRAFORM_PATH="$(command -v tofu)"
            export TF_ACC_PROVIDER_NAMESPACE="nico34638"
            export QUICKWIT_ENDPOINT="''${QUICKWIT_ENDPOINT:-http://localhost:7280}"
          '';
        };
      });
    };
}
